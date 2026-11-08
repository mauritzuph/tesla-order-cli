//go:build darwin

package urlhandler

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const lsregister = "/System/Library/Frameworks/CoreServices.framework/Frameworks/" +
	"LaunchServices.framework/Support/lsregister"

func Supported() bool { return true }

// Install builds, signs and registers the handler app. Safe to run repeatedly.
func Install(dataDir string) (string, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", err
	}
	source, err := AppleScriptSource(InboxPath(dataDir))
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "tesla-handler-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	script := filepath.Join(tmp, "handler.applescript")
	if err := os.WriteFile(script, []byte(source), 0o600); err != nil {
		return "", err
	}
	built := filepath.Join(tmp, AppName)
	if err := run("osacompile", "-o", built, script); err != nil {
		return "", err
	}
	plist := filepath.Join(built, "Contents", "Info.plist")
	for _, edit := range PlistEdits() {
		if err := run("plutil", append(edit, plist)...); err != nil {
			return "", err
		}
	}
	// Editing Info.plist invalidates osacompile's signature; re-sign ad hoc.
	if err := run("codesign", "--force", "--deep", "--sign", "-", built); err != nil {
		return "", err
	}

	app := AppPath(dataDir)
	if _, err := os.Stat(app); err == nil {
		_ = run(lsregister, "-u", app)
		if err := os.RemoveAll(app); err != nil {
			return "", err
		}
	}
	if err := os.Rename(built, app); err != nil {
		// tmp may be on another volume
		if err := run("mv", built, app); err != nil {
			return "", err
		}
	}
	if err := run(lsregister, "-f", app); err != nil {
		return "", err
	}
	if err := setDefaultHandler(); err != nil {
		return app, err
	}
	// LaunchServices persists the new default asynchronously; wait briefly so
	// Installed/CurrentHandler report it right after installing.
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if CurrentHandler() == BundleID {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	return app, nil
}

func Uninstall(dataDir string) (bool, error) {
	app := AppPath(dataDir)
	if _, err := os.Stat(app); err != nil {
		return false, nil
	}
	_ = run(lsregister, "-u", app)
	return true, os.RemoveAll(app)
}

// RemoveLegacy unregisters and deletes the pre-rename helper app, if present.
func RemoveLegacy(dataDir string) bool {
	app := filepath.Join(dataDir, legacyAppName)
	if _, err := os.Stat(app); err != nil {
		return false
	}
	_ = run(lsregister, "-u", app)
	return os.RemoveAll(app) == nil
}

func Installed(dataDir string) bool {
	_, err := os.Stat(AppPath(dataDir))
	return err == nil && CurrentHandler() == BundleID
}

// CurrentHandler reads the default tesla:// handler from LaunchServices' prefs.
func CurrentHandler() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	prefs := filepath.Join(home, "Library/Preferences/com.apple.LaunchServices/com.apple.launchservices.secure.plist")
	out, err := exec.Command("plutil", "-convert", "json", "-o", "-", prefs).Output()
	if err != nil {
		return ""
	}
	var data struct {
		LSHandlers []map[string]any `json:"LSHandlers"`
	}
	if json.Unmarshal(out, &data) != nil {
		return ""
	}
	for _, h := range data.LSHandlers {
		if s, _ := h["LSHandlerURLScheme"].(string); strings.EqualFold(s, Scheme) {
			role, _ := h["LSHandlerRoleAll"].(string)
			return strings.ToLower(role)
		}
	}
	return ""
}

// setDefaultHandler calls LSSetDefaultHandlerForURLScheme via JavaScript for
// Automation, which avoids cgo (and keeps cross-compilation simple).
func setDefaultHandler() error {
	script := fmt.Sprintf(`ObjC.import("CoreServices"); $.LSSetDefaultHandlerForURLScheme($(%q), $(%q))`, Scheme, BundleID)
	out, err := exec.Command("osascript", "-l", "JavaScript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("could not set the tesla:// handler: %s", strings.TrimSpace(string(out)))
	}
	if status := strings.TrimSpace(string(out)); status != "0" && status != "" {
		return fmt.Errorf("macOS refused to set the tesla:// handler (OSStatus %s)", status)
	}
	return nil
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %s", filepath.Base(name), strings.TrimSpace(string(out)))
	}
	return nil
}
