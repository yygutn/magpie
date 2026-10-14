# Local session usage

magpie reads the agents' own session files to show, beside what its gateway
served, the calls each agent made on its own: the Sessions page ("使用 →
会话"), the TUI's sessions list, and `magpie usage`'s second table. This
reference describes how a session is found and read, which is where a new
agent's support is added.

## Responsibilities and boundaries

- **Read only.** Every local store is opened read-only. magpie never
  writes, moves or deletes an agent's sessions. A store with no command to
  resume a session is listed with `Session.ReadOnly` set, and no
  `Session.Resume` line.
- **Two consumers, one parse.** A file's parse is kept once and read by
  both paths:
  - `allFiles()` (sessions.go) feeds `List`, `StatsFor`/`StatsAt` and the
    `/api/sessions*` endpoints — the Sessions page.
  - `callSources()` (calls.go) feeds `sessions.Calls`, which
    `internal/usage`'s ledger reads as "direct" calls (`Source == "log"`) —
    `magpie usage` and the Requests page.
  An agent that should appear in both needs an entry in each; the readers
  share their file discovery.

## Source of truth

- `internal/sessions/sessions.go` — `allFiles`, `parserFor` (the parser
  dispatch), `assemble` (state → Session), `state`/`day` (the aggregates).
- `internal/sessions/calls.go` — `Call`, `callSources`, `readCalls`.
- `internal/sessions/call_cache.go` — `wholeCallReaders` and
  `callCacheVersion`.
- `internal/usage/ledger.go` — `logRecord`, `gatewayMatches` (pairing a
  gateway call with its session-file record).

## Adding an agent

A file is read one of two ways, decided by `parserFor`:

- **Line by line** (Claude Code's shape, `claude: true`): the default. The
  file is JSONL whose reply lines repeat a call's usage; `claudeLine` folds
  them by message id.
- **Whole** (`sessionParser{whole: …}`): a database, a packed file, or one
  that cannot be read from the middle. The whole file is parsed into a
  `state`.

For the second kind, the pieces are:

1. **Discovery** — a `*Files() []file` returning one `file{agent, key,
   path, main, …}` per session. Set `size` and `mod` so a changed session is
   reread and an unchanged one is not; for a SQLite store whose writes land
   in the WAL, include the `-wal`/`-shm` sizes and times.
2. **Admission** — add the agent to `allFiles()` (Sessions page) and, to
   reach `magpie usage`, to `callSources()` plus an entry in
   `wholeCallReaders`.
3. **Parsing** — either a `whole` parser in `parserFor`, or a call reader
   `func(file) (*callFile, bool)`.
4. **Cache version** — bump `callCacheVersion` when the call records
   change, with a line saying what the new version names.

`internal/sessions/antigravity.go` (Antigravity, agent id `agy`) is a
worked example of the second kind: a per-conversation SQLite database read
whole, whose token figures are in a protobuf BLOB.

## Antigravity (`agy`)

Antigravity keeps each conversation as its own SQLite database under
`$GEMINI_CLI_HOME/.gemini/antigravity/conversations/<uuid>.db`, else
`~/.gemini/antigravity/conversations/`, beside `conversation_summaries.db`.
The messages are encrypted at rest; two plain protobuf BLOBs are read
instead — a step's token usage (`steps.metadata`) and the model a generator
ran (`gen_metadata.data`) — through a wire-format parser
(`antigravity_proto.go`, as `devin_images.go` does for its vendor).

- **Folding.** One model call can be split over several steps (a tool loop's
  before and after), and the store repeats the call's id across them.
  Counting every step would count one request more than once, so the steps
  sharing a key are folded to the **last** of them, keyed by response id,
  else message id, else step index. Three steps, two responses, two calls.
- **Unreadable metadata is not empty.** A step whose model the generators
  do not name is an error: the figures would be silently wrong, so the
  session is treated as unreadable rather than counted as zero.

### Pairing a call through magpie with its local record

A request agy sends through magpie is logged by the gateway *and* written
into a conversation database. Both must not count it. The gateway's
`ResponseID` and the store's `steps.metadata` response id are the same
Gemini `responseId`, so the ledger pairs them by it:

- `logRecord` copies `Call.ResponseID` into the record for `agy` (agy sends
  no session header, so the session + tokens + time fallback cannot match).
- `matchedBlocks` (request_page.go) has a second pass that pairs a gateway
  row with a session-file row by `ResponseID`, after the `RequestID` pass.
  The matched local row is dropped; the gateway row is kept.

A local call with no gateway record (agy pointed straight at Google, or the
gateway record aged out) is counted on its own with `Source == "log"`.

### Pricing

The store names the deployment `gemini-3.8-flash-n`, an id the catalog does
not list. `provider.pricedNames` prices it as
`gemini-3.8-flash` (its family id), the same equivalence the gateway's
`GeminiServing` rule states. An exact catalog price for the `-n` id wins
over the alias.

## Verification

```sh
go test -tags nogui ./internal/sessions ./internal/usage ./internal/provider
```

The tests that pin this behavior:

- `internal/sessions/antigravity_test.go` —
  `TestAntigravityFoldsStepsByResponse` (three steps fold to two calls),
  `TestAntigravitySessionTokens` (the Sessions page's aggregates),
  `TestAntigravityStats` (StatsAt includes the agy session),
  `TestAntigravityCallsSource` (one call per folded response),
  `TestAntigravityUnreadableMetadataIsNotEmpty`.
- `internal/usage/antigravity_dedup_test.go` —
  `TestAntigravityGatewayAndLocalCountedOnce` (a gateway call and its local
  record count once), `TestAntigravityLocalOnlyCounted`.
- `internal/provider/antigravity_price_test.go` —
  `TestAntigravityNDeploymentIsPricedAsFamily`.
