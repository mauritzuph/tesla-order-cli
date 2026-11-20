package locations

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAndLookup(t *testing.T) {
	t.Cleanup(func() { known = nil })
	path := write(t, `{
		"1234": {"display_name": "Somewhere Center", "phone": "+49 30 1",
		         "address": {"address_1": "Main St 1", "city": "Berlin", "postal_code": "10115", "country": "DE"},
		         "store_email": null},
		"99": "Just a name"
	}`)
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	want := Location{Name: "Somewhere Center", Address: "Main St 1, 10115 Berlin", Phone: "+49 30 1"}
	for _, id := range []any{"1234", 1234.0, " 1234 "} {
		if got, ok := Lookup(id); !ok || got != want {
			t.Errorf("Lookup(%v) = %+v, %v", id, got, ok)
		}
	}
	if got, ok := Lookup("99"); !ok || got != (Location{Name: "Just a name"}) {
		t.Errorf("name only: %+v", got)
	}
	for _, id := range []any{"5", nil, true} {
		if _, ok := Lookup(id); ok {
			t.Errorf("Lookup(%v) should miss", id)
		}
	}

	// a missing file clears the list
	if err := Load(filepath.Join(t.TempDir(), FileName)); err != nil {
		t.Fatal(err)
	}
	if _, ok := Lookup("99"); ok {
		t.Error("still known after loading a missing file")
	}
}

func TestLoadInvalid(t *testing.T) {
	t.Cleanup(func() { known = nil })
	for _, content := range []string{`not json`, `{"1": 5}`} {
		if err := Load(write(t, content)); err == nil {
			t.Errorf("%s: want error", content)
		}
		if known != nil {
			t.Errorf("%s: partial list kept", content)
		}
	}
}
