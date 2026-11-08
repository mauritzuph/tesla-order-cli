package urlhandler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAppleScriptEmbedsInboxAndQuotesURL(t *testing.T) {
	src, err := AppleScriptSource("/Users/me/My Data/callback.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"on open location theURL", `quoted form of "/Users/me/My Data/callback.txt"`, "quoted form of theURL"} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{`/tmp/a"b`, `/tmp/a\b`} {
		if _, err := AppleScriptSource(bad); err == nil {
			t.Errorf("%s should be rejected", bad)
		}
	}
}

func TestPlistEditsRegisterScheme(t *testing.T) {
	joined := ""
	for _, e := range PlistEdits() {
		joined += strings.Join(e, " ") + "\n"
	}
	for _, want := range []string{"CFBundleIdentifier -string " + BundleID, "LSUIElement -bool true", `"CFBundleURLSchemes":["tesla"]`} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestSupportedOnlyOnMacOS(t *testing.T) {
	if Supported() != (runtime.GOOS == "darwin") {
		t.Error("Supported() is wrong for this OS")
	}
	if runtime.GOOS != "darwin" {
		if _, err := Install(t.TempDir()); err != ErrUnsupported {
			t.Errorf("got %v", err)
		}
	}
}

// Compiles the applet and applies the plist edits; nothing is registered.
func TestAppletBuildsWithoutRegistering(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("needs osacompile")
	}
	dir := t.TempDir()
	src, _ := AppleScriptSource(filepath.Join(dir, "callback.txt"))
	script := filepath.Join(dir, "h.applescript")
	_ = os.WriteFile(script, []byte(src), 0o600)
	app := filepath.Join(dir, "h.app")
	if out, err := exec.Command("osacompile", "-o", app, script).CombinedOutput(); err != nil {
		t.Fatalf("osacompile: %s", out)
	}
	plist := filepath.Join(app, "Contents", "Info.plist")
	for _, e := range PlistEdits() {
		if out, err := exec.Command("plutil", append(e, plist)...).CombinedOutput(); err != nil {
			t.Fatalf("plutil %v: %s", e, out)
		}
	}
	out, _ := exec.Command("plutil", "-extract", "CFBundleURLTypes.0.CFBundleURLSchemes.0", "raw", plist).Output()
	if strings.TrimSpace(string(out)) != "tesla" {
		t.Errorf("scheme not registered in plist: %q", out)
	}
}
