package provider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
)

// TestAntigravityNDeploymentIsPricedAsFamily: Google's Code Assist backend
// names the deployment gemini-3.8-flash-n in modelVersion. The catalog has
// no such id; the family id gemini-3.8-flash is the right list price.
func TestAntigravityNDeploymentIsPricedAsFamily(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(catalog.CachePath()), 0700); err != nil {
		t.Fatal(err)
	}
	defer catalog.Reset()
	if err := os.WriteFile(catalog.CachePath(), []byte(`{"google":{"id":"google","models":{"gemini-3.8-flash":{"id":"gemini-3.8-flash","cost":{"input":3,"output":7,"cache_read":1,"cache_write":2}}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	catalog.Reset()

	pr, ok := MakerPrice("gemini-3.8-flash-n")
	if !ok || pr.Input != 3 || pr.Output != 7 {
		t.Fatalf("gemini-3.8-flash-n price = %+v, ok=%v", pr, ok)
	}
	if PricedName("gemini-3.8-flash-n") != "gemini-3.8-flash" {
		t.Fatalf("PricedName = %q", PricedName("gemini-3.8-flash-n"))
	}

	// An exact catalog price wins over the alias.
	if err := os.WriteFile(catalog.CachePath(), []byte(`{"google":{"id":"google","models":{"gemini-3.8-flash-n":{"id":"gemini-3.8-flash-n","cost":{"input":4,"output":8}},"gemini-3.8-flash":{"id":"gemini-3.8-flash","cost":{"input":3,"output":7,"cache_read":1,"cache_write":2}}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	catalog.Reset()
	pr, ok = MakerPrice("gemini-3.8-flash-n")
	if !ok || pr.Input != 4 {
		t.Fatalf("exact-n price lost: %+v, ok=%v", pr, ok)
	}
	if PricedName("gemini-3.8-flash-n") != "gemini-3.8-flash-n" {
		t.Fatalf("PricedName must agree with exact-name price: %q", PricedName("gemini-3.8-flash-n"))
	}
}
