package options

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLabel(t *testing.T) {
	if got := Label("PPSW"); got != "Paint: Pearl White Multi-Coat" {
		t.Errorf("PPSW = %q", got)
	}
	if got := Label(" apbs "); got != "Autopilot" {
		t.Errorf("apbs = %q", got)
	}
	if got := Label("XXXX"); got != "" {
		t.Errorf("unknown = %q", got)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Cleanup(func() { overrides = nil })
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(`{"ppsw": "My white", "ZZ01": "Something new"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadOverrides(path); err != nil {
		t.Fatal(err)
	}
	if Label("PPSW") != "My white" || Label("ZZ01") != "Something new" || Label("APBS") != "Autopilot" {
		t.Errorf("overrides not applied: %q %q %q", Label("PPSW"), Label("ZZ01"), Label("APBS"))
	}

	// a missing file clears them again
	if err := LoadOverrides(filepath.Join(t.TempDir(), FileName)); err != nil || Label("ZZ01") != "" {
		t.Errorf("err %v, ZZ01 = %q", err, Label("ZZ01"))
	}

	if err := os.WriteFile(path, []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadOverrides(path); err == nil {
		t.Error("want error for invalid JSON")
	}
}
