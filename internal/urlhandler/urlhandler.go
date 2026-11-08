// Package urlhandler registers a macOS handler for tesla:// URLs, so login
// works in the normal browser.
//
// Tesla redirects to tesla://auth/callback?code=... after login. Browsers can't
// open that themselves but hand unknown URL schemes to whatever app macOS has
// registered for them. Install builds a tiny AppleScript applet that registers
// for tesla:// and writes the URL it receives to a file, where `login` picks it
// up. Only macOS is supported; elsewhere login falls back to pasting the URL.
package urlhandler

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	Scheme   = "tesla"
	AppName  = "TeslaOrderCLICallback.app"
	BundleID = "io.github.mauritzuph.tesla-order-cli.callback"

	// legacyAppName is the helper from before the rename. Its script writes to
	// the old data folder, so it is removed (login installs the new one).
	legacyAppName = "TeslaOrderTrackerCallback.app"
)

var ErrUnsupported = errors.New("the tesla:// helper is only available on macOS")

func AppPath(dataDir string) string   { return filepath.Join(dataDir, AppName) }
func InboxPath(dataDir string) string { return filepath.Join(dataDir, "callback.txt") }

// AppleScriptSource is the applet's script. The received URL only ever reaches
// the shell through `quoted form of`.
func AppleScriptSource(inbox string) (string, error) {
	if strings.ContainsAny(inbox, "\"\\") {
		return "", fmt.Errorf("unsupported characters in data directory path: %s", inbox)
	}
	return fmt.Sprintf(`on open location theURL
    do shell script "umask 077; printf '%%s' " & quoted form of theURL & ¬
        " > " & quoted form of "%s"
end open location

on run
    display dialog "This helper receives the Tesla login callback for " & ¬
        "tesla-order-cli. Run `+"`tesla-order-cli login`"+` instead of " & ¬
        "opening it directly." buttons {"OK"} default button 1
end run
`, inbox), nil
}

// PlistEdits are the plutil edits that register the URL scheme and hide the
// applet from the Dock.
func PlistEdits() [][]string {
	return [][]string{
		{"-replace", "CFBundleIdentifier", "-string", BundleID},
		{"-replace", "CFBundleName", "-string", "Tesla Order CLI Callback"},
		{"-replace", "LSUIElement", "-bool", "true"},
		{"-replace", "CFBundleURLTypes", "-json",
			`[{"CFBundleURLName":"Tesla login callback","CFBundleURLSchemes":["` + Scheme + `"]}]`},
	}
}
