package sessions

import (
	"sort"
	"strconv"
	"strings"
)

// parseAntigravity reads one Antigravity conversation's usage whole, from
// its own SQLite database (antigravity.go says what it holds). Only the
// metadata is read: a step's tokens and time, and the model its generator
// ran. The conversation's messages are encrypted and are not touched, so
// there are no prompts, replies or tools to count — the figures are the
// tokens each model call spent.
//
// The session is listed read only: Antigravity's GUI and CLI write the
// store, and neither has a command to pick a conversation up again.
func parseAntigravity(f file) *state {
	s := &state{Size: f.size, Mod: f.mod.UnixNano()}
	usages, named, cwd, err := agyRead(f)
	if err != nil {
		// unreadable metadata is not an empty conversation: the figures
		// would be wrong, not merely missing (cc-bar keeps the old state)
		return &state{}
	}
	s.Cwd = cwd
	// s.Title is left empty when Antigravity has not named the conversation:
	// assemble() falls back to the first prompt when there is one, else the
	// session is named after the agent (see Session.Title handling there).
	if t := title(named); t != "" {
		s.Title = t
	}
	for _, u := range usages {
		s.saw(u.at, true)
		s.use(dateOf(u.at), u.model, u.tokens)
	}
	return s
}

// antigravityCallFiles are Antigravity's conversations as call sources: the
// same files the Sessions page reads, so a session that has not changed is
// not read again.
func antigravityCallFiles() []file { return agyFiles() }

// readAntigravityCalls reads one conversation's calls whole: each model
// call, after the steps of one response have been folded to the last of
// them (antigravity.go), is one call. Its key is that same fold, so the
// gateway's own record of a call made through magpie can be recognized by
// response id (see usage.logRecord and the ledger's matcher).
func readAntigravityCalls(f file) (*callFile, bool) {
	st := &callFile{Agent: "agy", Path: f.path, Session: strings.TrimPrefix(f.key, "agy:")}
	usages, _, _, err := agyRead(f)
	if err != nil {
		return st, false
	}
	for _, u := range usages {
		st.Calls = append(st.Calls, Call{
			Agent:      "agy",
			Session:    st.Session,
			Model:      u.model,
			Time:       u.at,
			Tokens:     u.tokens,
			ResponseID: u.responseID,
			// Msg is read as a global key: calls.go's seen map and the
			// Requests page's visibleLocal both drop a call whose Msg was
			// seen in another file. The fold key alone is only unique within
			// one conversation, so it is named by the conversation here — two
			// conversations each with a step of no response or message id
			// then keep both their calls.
			Msg: st.Session + ":" + agyKey(u),
		})
	}
	sort.SliceStable(st.Calls, func(i, j int) bool {
		if !st.Calls[i].Time.Equal(st.Calls[j].Time) {
			return st.Calls[i].Time.Before(st.Calls[j].Time)
		}
		return st.Calls[i].Msg < st.Calls[j].Msg
	})
	return st, true
}

// agyKey is a call's fold key: its response id, else its message id, else
// its step index — the same key agySteps folds by.
func agyKey(u agyUsage) string {
	if u.responseID != "" {
		return "response:" + u.responseID
	}
	if u.messageID != "" {
		return "message:" + u.messageID
	}
	return "step:" + strconv.FormatInt(u.idx, 10)
}
