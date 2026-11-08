package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mauritzuph/tesla-order-cli/internal/auth"
)

func TestTokensRoundtripWithPrivatePermissions(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "data"))
	if s.LoadTokens() != nil {
		t.Fatal("expected no tokens")
	}
	want := auth.Tokens{AccessToken: "a", RefreshToken: "r", ExpiresAt: 123}
	if err := s.SaveTokens(want); err != nil {
		t.Fatal(err)
	}
	if got := s.LoadTokens(); got == nil || *got != want {
		t.Fatalf("got %+v", got)
	}
	if runtime.GOOS == "windows" {
		return // Unix permission bits don't apply
	}
	info, _ := os.Stat(s.TokensPath())
	dir, _ := os.Stat(s.Dir)
	if info.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
		t.Errorf("modes %v %v", info.Mode().Perm(), dir.Mode().Perm())
	}
}

func TestCorruptFilesAreIgnored(t *testing.T) {
	s := New(t.TempDir())
	_ = os.WriteFile(s.TokensPath(), []byte("{not json"), 0o600)
	_ = os.WriteFile(s.SnapshotPath(), []byte("[1, 2]"), 0o600)
	if s.LoadTokens() != nil || s.LoadSnapshot() != nil {
		t.Fatal("corrupt files should be ignored")
	}
}

func TestWriteLeavesNoTempFilesAndDelete(t *testing.T) {
	s := New(t.TempDir())
	_ = s.SaveSnapshot(Snapshot{Orders: map[string]any{}})
	_ = s.SaveSnapshot(Snapshot{Orders: map[string]any{"x": 1.0}})
	entries, _ := os.ReadDir(s.Dir)
	if len(entries) != 1 || entries[0].Name() != "orders.json" {
		t.Fatalf("entries %v", entries)
	}
	if got := s.LoadSnapshot(); got.Orders["x"] != 1.0 {
		t.Fatalf("got %+v", got)
	}
	if deleted, _ := s.DeleteTokens(); deleted {
		t.Error("nothing to delete")
	}
	_ = s.SaveTokens(auth.Tokens{AccessToken: "a", RefreshToken: "r"})
	if deleted, _ := s.DeleteTokens(); !deleted || s.LoadTokens() != nil {
		t.Error("delete failed")
	}
}

func TestDefaultDir(t *testing.T) {
	t.Setenv(EnvDataDir, "/tmp/x")
	if DefaultDir() != "/tmp/x" {
		t.Error("env override ignored")
	}
	t.Setenv(EnvDataDir, "")
	if filepath.Base(DefaultDir()) != ".tesla-order-cli" {
		t.Errorf("got %s", DefaultDir())
	}
}

func TestMigrateLegacyDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	newDir := filepath.Join(home, ".tesla-order-cli")

	if old, err := MigrateLegacyDir(newDir); old != "" || err != nil {
		t.Fatalf("nothing to migrate: %q %v", old, err)
	}

	legacy := filepath.Join(home, ".tesla-order-tracker")
	_ = os.MkdirAll(legacy, 0o700)
	_ = os.WriteFile(filepath.Join(legacy, "tokens.json"), []byte(`{"access_token":"a","refresh_token":"r","expires_at":1}`), 0o600)
	old, err := MigrateLegacyDir(newDir)
	if err != nil || old != legacy {
		t.Fatalf("got %q %v", old, err)
	}
	if New(newDir).LoadTokens() == nil {
		t.Error("tokens not moved")
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Error("old folder should be gone")
	}

	// Never overwrite an existing new folder.
	_ = os.MkdirAll(legacy, 0o700)
	if old, _ := MigrateLegacyDir(newDir); old != "" {
		t.Error("must not migrate when the new folder exists")
	}
}
