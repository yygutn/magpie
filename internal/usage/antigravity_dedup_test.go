package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/sessions"
)

// TestAntigravityGatewayAndLocalCountedOnce: when agy makes a request
// through magpie, the gateway log and the local SQLite step share the same
// Gemini response id. The local record must not add to the totals.
func TestAntigravityGatewayAndLocalCountedOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	os.MkdirAll(filepath.Dir(catalog.CachePath()), 0o755)
	os.WriteFile(catalog.CachePath(), []byte(`{"google":{"id":"google","models":{"gemini-3.8-flash":{"id":"gemini-3.8-flash","cost":{"input":3,"output":7,"cache_read":1,"cache_write":2}}}}}`), 0o644)
	catalog.Reset()
	t.Cleanup(catalog.Reset)

	now := holdClock(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local))

	// gateway record: the call agy made through magpie
	Append(Record{Time: now.Add(-5 * time.Minute), Agent: "agy", Provider: "antigravity", Model: "gemini-3.8-flash", Input: 100, Output: 20,
		Millis: 4000, Status: 200, ResponseID: "resp_agy_1", Endpoint: "/v1/chat/completions"})

	// local record of the same call: same response id, same tokens
	calls := []sessions.Call{
		{Time: now.Add(-5 * time.Minute), Agent: "agy", Session: "s1", Model: "gemini-3.8-flash-n", ResponseID: "resp_agy_1",
			Tokens: sessions.Tokens{Input: 100, Output: 20}},
	}
	old := LogCalls
	LogCalls = func(time.Time) []sessions.Call { return calls }
	t.Cleanup(func() { LogCalls = old })

	rows, sum, agents := Ledger(Month, Filter{})
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if sum.Calls != 1 || sum.Input != 100 || sum.Output != 20 {
		t.Fatalf("sum = %+v", sum)
	}
	if len(agents) != 1 || agents[0] != "agy" {
		t.Fatalf("agents = %v", agents)
	}
	if rows[0].Source != "" { // gateway source is the one kept
		t.Fatalf("kept row source = %q", rows[0].Source)
	}
}

// TestAntigravityLocalOnlyCounted: a call only in the local SQLite store
// (agy was pointed straight at Google, or the gateway record aged out) is
// still counted, with Source "log".
func TestAntigravityLocalOnlyCounted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	os.MkdirAll(filepath.Dir(catalog.CachePath()), 0o755)
	os.WriteFile(catalog.CachePath(), []byte(`{"google":{"id":"google","models":{"gemini-3.8-flash":{"id":"gemini-3.8-flash","cost":{"input":3,"output":7,"cache_read":1,"cache_write":2}}}}}`), 0o644)
	catalog.Reset()
	t.Cleanup(catalog.Reset)

	now := holdClock(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local))

	calls := []sessions.Call{
		{Time: now.Add(-5 * time.Minute), Agent: "agy", Session: "s1", Model: "gemini-3.8-flash-n", ResponseID: "resp_local",
			Tokens: sessions.Tokens{Input: 50, Output: 10}},
	}
	old := LogCalls
	LogCalls = func(time.Time) []sessions.Call { return calls }
	t.Cleanup(func() { LogCalls = old })

	rows, sum, _ := Ledger(Month, Filter{})
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Source != "log" {
		t.Fatalf("source = %q", rows[0].Source)
	}
	if sum.Calls != 1 || sum.Input != 50 || sum.Output != 10 {
		t.Fatalf("sum = %+v", sum)
	}
}
