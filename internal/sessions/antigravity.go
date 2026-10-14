package sessions

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/provider"
)

// Antigravity (Google's Code Assist client, and its CLI agy) keeps every
// conversation as its own SQLite database under ~/.gemini/antigravity/
// conversations/<uuid>.db, beside one conversation_summaries.db holding
// every conversation's title and its workspace folder. The databases are
// written all the while Antigravity runs, in WAL mode. The conversation's
// messages are encrypted at rest, but two plain protobuf BLOBs carry what
// magpie reads: a step's timestamp and token usage (steps.metadata) and the
// model a generator ran (gen_metadata.data). Only those figures are read —
// no message body — through a read-only connection. antigravity_proto.go
// parses the wire format itself.
//
//	steps         idx, metadata (StepMetadata), …: one row per step
//	gen_metadata  idx, data (GeneratorMetadata), …: one row per model call,
//	              naming its model and the steps it owns
//
// Antigravity's GUI and CLI write this same store, so a session is listed
// read only: no agent command picks one up again.
//
// One model call can be split over several steps (a tool loop's before and
// after), and the store repeats the call's own id across them: counting
// every step would count one request more than once. A step is keyed by its
// response id, else its message id, else its own index, and the last step
// of a key replaces the ones before it — cc-bar's AntigravitySQLiteScanner
// folds three steps into two entries the same way.

// agyStore is Antigravity's conversation databases while a listing reads
// them: the summaries database, read once (done), and the conversations,
// each opened for its own read rather than through the listing's shared
// handles — a whole-file parse runs on several workers at once, and the
// shared handle table is not safe for them.
type agyStore struct {
	done      sync.Once
	summaries map[string]agySummary
}

// agySummary is one conversation's entry in conversation_summaries.db.
type agySummary struct {
	title string
	cwd   string
}

// AntigravityDir is Antigravity's data folder: $GEMINI_CLI_HOME/.gemini/
// antigravity, else ~/.gemini/antigravity — the folder its GUI and the agy
// CLI share. It is Gemini CLI's own variable, as both keep their stores
// under ~/.gemini (geminiTraceFiles reads it too).
func AntigravityDir() string {
	home, _ := os.UserHomeDir()
	if custom := appdir.Getenv("GEMINI_CLI_HOME"); custom != "" {
		home = custom
	}
	return filepath.Join(home, ".gemini", "antigravity")
}

// agyConversationsDir is the folder of the conversations' databases.
func agyConversationsDir() string { return filepath.Join(AntigravityDir(), "conversations") }

// agySummaryPath is the conversations' summaries database.
func agySummaryPath() string { return filepath.Join(AntigravityDir(), "conversation_summaries.db") }

// agyFiles are Antigravity's conversations, one database a session: its
// path the database's, its size and time those of the database and its WAL
// (so a WAL-only write is seen), and readOnly set.
func agyFiles() []file {
	dir := agyConversationsDir()
	entries := readDirectory(dir)
	if len(entries) == 0 {
		return nil
	}
	store := &agyStore{}
	var out []file
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		f := file{
			agent:    "agy",
			key:      "agy:" + strings.TrimSuffix(e.Name(), ".db"),
			path:     filepath.Join(dir, e.Name()),
			sid:      strings.TrimSuffix(e.Name(), ".db"),
			main:     true,
			readOnly: true,
			agy:      store,
		}
		if !agyStat(&f) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// agyStat fills f's size and time from the database and its WAL: a write can
// land in the WAL and leave the database file untouched, so both are summed
// and the later time kept. The WAL's -shm is left out — SQLite writes it on
// every read, so including it would reread an unchanged conversation. It
// reports whether the database is there.
func agyStat(f *file) bool {
	fi, err := os.Stat(f.path)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	size, mod := fi.Size(), fi.ModTime()
	if wi, err := os.Stat(f.path + "-wal"); err == nil {
		size += wi.Size()
		if wi.ModTime().After(mod) {
			mod = wi.ModTime()
		}
	}
	f.size, f.mod = size, mod
	return true
}

// agyReadSummaries reads conversation_summaries.db once per listing: each
// conversation's title and workspace folder. A conversation missing from it
// has neither.
func agyReadSummaries() map[string]agySummary {
	out := map[string]agySummary{}
	path := agySummaryPath()
	if !fileExists(path) {
		return out
	}
	db, err := provider.OpenReadOnly(path)
	if err != nil {
		return out
	}
	defer db.Close()
	if !ocHasTable(db, "conversation_summaries") {
		return out
	}
	rows, err := db.Query("SELECT conversation_id, COALESCE(title, ''), COALESCE(workspace_uris, '') FROM conversation_summaries")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, title, uris string
		if rows.Scan(&id, &title, &uris) != nil || id == "" {
			continue
		}
		out[id] = agySummary{title: title, cwd: agyWorkspacePath(uris)}
	}
	return out
}

// agyWorkspacePath is the first file:// folder workspace_uris names, "" for
// none. Other uri kinds and project ids are left out.
func agyWorkspacePath(raw string) string {
	raw = strings.TrimSpace(raw)
	var uris []struct {
		Scheme string `json:"scheme"`
		Host   string `json:"host"`
		Path   string `json:"path"`
	}
	if json.Unmarshal([]byte(raw), &uris) == nil {
		for _, u := range uris {
			if u.Scheme == "file" && (u.Host == "" || u.Host == "localhost") && u.Path != "" {
				return u.Path
			}
		}
		return ""
	}
	var plain []string
	if json.Unmarshal([]byte(raw), &plain) == nil {
		for _, s := range plain {
			if p, ok := strings.CutPrefix(s, "file://"); ok {
				if p = strings.TrimPrefix(p, "localhost"); p != "" {
					return p
				}
			}
		}
	}
	return ""
}

// summariesOf reads the summaries database once per listing, however many
// workers ask (a listing parses conversations on several at once).
func (st *agyStore) summariesOf() map[string]agySummary {
	st.done.Do(func() { st.summaries = agyReadSummaries() })
	return st.summaries
}

// agyUsage is one model call's usage in a conversation, after the steps of
// one response have been folded into one.
type agyUsage struct {
	model      string
	at         time.Time
	tokens     Tokens
	responseID string
	messageID  string
	idx        int64
}

// agyRead reads one conversation: its model calls (one per response), its
// title and folder.
func agyRead(f file) (usages []agyUsage, title, cwd string, err error) {
	// Opened for this read alone: a whole-file parse runs on several workers
	// at once, so it must not reach the listing's shared handle table.
	db, err := provider.OpenReadOnly(f.path)
	if err != nil {
		return nil, "", "", err
	}
	defer db.Close()
	if !ocHasTable(db, "steps") || !ocHasTable(db, "gen_metadata") {
		return nil, "", "", errAntigravityMetadata
	}
	models, err := agyModels(db)
	if err != nil {
		return nil, "", "", err
	}
	usages, err = agySteps(db, models)
	if err != nil {
		return nil, "", "", err
	}
	if f.agy != nil {
		if s, ok := f.agy.summariesOf()[f.sid]; ok {
			title, cwd = s.title, s.cwd
		}
	}
	return usages, title, cwd, nil
}

// agyModels maps each step index to the model the generator owning it named.
func agyModels(db *sql.DB) (map[int64]string, error) {
	rows, err := db.Query("SELECT data FROM gen_metadata")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var b []byte
		if rows.Scan(&b) != nil {
			return nil, errAntigravityMetadata
		}
		g, err := agParseGenerator(b)
		if err != nil {
			return nil, err
		}
		if g.model == "" {
			continue
		}
		for _, idx := range g.indices {
			out[idx] = g.model
		}
	}
	return out, rows.Err()
}

// agySteps reads a conversation's steps and folds the ones sharing a key to
// the last of them, in the order their steps come. A step without tokens is
// skipped; one whose model the generators do not name is an error (the
// conversation's figures would be wrong, not merely incomplete).
func agySteps(db *sql.DB, models map[int64]string) ([]agyUsage, error) {
	rows, err := db.Query("SELECT idx, metadata FROM steps WHERE metadata IS NOT NULL ORDER BY idx")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type slot struct {
		order int
		use   agyUsage
	}
	folded := map[string]*slot{}
	var order []string
	for rows.Next() {
		var idx int64
		var b []byte
		if rows.Scan(&idx, &b) != nil {
			return nil, errAntigravityMetadata
		}
		u, err := agParseStep(b)
		if err != nil {
			return nil, err
		}
		if !u.hasTokens() {
			continue
		}
		model, ok := models[idx]
		if !ok || model == "" {
			return nil, errAntigravityMetadata
		}
		if u.at == 0 {
			return nil, errAntigravityMetadata
		}
		key := u.responseID
		if key == "" {
			key = u.messageID
		}
		if key == "" {
			key = "step:" + strconv.FormatInt(idx, 10)
		}
		use := agyUsage{
			model: model, at: time.Unix(u.at, int64(u.nanos)),
			tokens:     Tokens{Input: u.input, Output: u.output, CacheRead: u.cacheRead, CacheWrite: u.cacheWrite},
			responseID: u.responseID, messageID: u.messageID, idx: idx,
		}
		if s := folded[key]; s != nil {
			s.use = use // the last step of a response replaces the ones before it
			continue
		}
		folded[key] = &slot{order: len(order), use: use}
		order = append(order, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]agyUsage, 0, len(order))
	for _, key := range order {
		out = append(out, folded[key].use)
	}
	return out, nil
}
