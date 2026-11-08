package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mauritzuph/tesla-order-cli/internal/api"
	"github.com/mauritzuph/tesla-order-cli/internal/auth"
	"github.com/mauritzuph/tesla-order-cli/internal/callback"
	"github.com/mauritzuph/tesla-order-cli/internal/httpx"
	"github.com/mauritzuph/tesla-order-cli/internal/store"
	"github.com/mauritzuph/tesla-order-cli/internal/testutil"
	"github.com/mauritzuph/tesla-order-cli/internal/urlhandler"
)

type harness struct {
	t        *testing.T
	dir      string
	http     *testutil.FakeHTTP
	stdout   bytes.Buffer
	stderr   bytes.Buffer
	stdin    string
	opened   []string // "browser|url"
	deps     Deps
	store    *store.Store
	clip     *stubSource
	handler  bool // pretend helper is installed
	installs int
}

// lazyStdin reads h.stdin only when the CLI asks, so a simulated browser can
// produce the callback URL first.
type lazyStdin struct {
	h *harness
	r io.Reader
}

func (l *lazyStdin) Read(p []byte) (int, error) {
	if l.r == nil {
		l.r = strings.NewReader(l.h.stdin)
	}
	return l.r.Read(p)
}

type stubSource struct{ text string }

func (s *stubSource) Poll() string { return s.text }

const future = 4_000_000_000

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, dir: t.TempDir(), clip: &stubSource{}}
	h.http = testutil.NewFakeHTTP(map[string]testutil.Route{
		api.OrdersURL: {Value: map[string]any{"response": []any{map[string]any{"referenceNumber": "RN1", "orderStatus": "BOOKED"}}}},
		api.TasksURL:  {Value: map[string]any{"tasks": map[string]any{}}},
		auth.TokenURL: {Value: map[string]any{"access_token": "a", "refresh_token": "r", "expires_in": 28800}},
	})
	h.store = store.New(h.dir)
	h.deps = Deps{
		NewHTTP:      func(httpx.Recorder) (httpx.Client, error) { return h.http, nil },
		OpenBrowser:  func(url, browser string) error { h.opened = append(h.opened, browser+"|"+url); return nil },
		NewClipboard: func() (callback.Source, bool) { return h.clip, true },
		Handler: HandlerOps{
			Supported: func() bool { return true },
			Installed: func(string) bool { return h.handler },
			Install:   func(string) (string, error) { h.installs++; h.handler = true; return "app", nil },
			Uninstall: func(string) (bool, error) { return true, nil },
			Current:   func() string { return urlhandler.BundleID },
		},
		Clock: callback.Clock{Sleep: func(time.Duration) {}},
	}
	return h
}

func (h *harness) run(args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	d := h.deps
	d.Stdout, d.Stderr, d.Stdin = &h.stdout, &h.stderr, &lazyStdin{h: h}
	return Execute(append([]string{"--data-dir", h.dir}, args...), d)
}

func (h *harness) loggedIn() {
	_ = h.store.SaveTokens(auth.Tokens{AccessToken: testutil.JWT(future), RefreshToken: "r", ExpiresAt: future})
}

func stateOf(url string) string {
	_, after, _ := strings.Cut(url, "state=")
	state, _, _ := strings.Cut(after, "&")
	return state
}

func TestManualLogin(t *testing.T) {
	h := newHarness(t)
	h.deps.OpenBrowser = func(url, _ string) error {
		h.stdin = auth.RedirectURI + "?code=c1&state=" + stateOf(url) + "\n"
		return nil
	}
	if code := h.run("login", "--manual"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if h.store.LoadTokens().RefreshToken != "r" || h.http.CallsTo(auth.TokenURL)[0].Body.(map[string]string)["code"] != "c1" {
		t.Error("tokens not exchanged")
	}
}

func TestLoginWithWrongURLFails(t *testing.T) {
	h := newHarness(t)
	h.stdin = "https://nope\n"
	if code := h.run("login", "--manual"); code != 1 || !strings.Contains(h.stderr.String(), "Login failed") {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if h.store.LoadTokens() != nil {
		t.Error("no tokens expected")
	}
}

func TestBrowserLoginViaHandlerInbox(t *testing.T) {
	h := newHarness(t)
	h.handler = true
	h.deps.OpenBrowser = func(url, _ string) error {
		return os.WriteFile(urlhandler.InboxPath(h.dir), []byte(auth.RedirectURI+"?code=from-handler&state="+stateOf(url)), 0o600)
	}
	if code := h.run("login", "--timeout", "2s"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if got := h.http.CallsTo(auth.TokenURL)[0].Body.(map[string]string)["code"]; got != "from-handler" {
		t.Errorf("code %q", got)
	}
	if _, err := os.Stat(urlhandler.InboxPath(h.dir)); !errors.Is(err, os.ErrNotExist) {
		t.Error("inbox should be consumed")
	}
}

func TestLoginInstallsHandlerAndFallsBackToClipboard(t *testing.T) {
	h := newHarness(t)
	h.deps.OpenBrowser = func(url, _ string) error {
		h.clip.text = auth.RedirectURI + "?code=from-clipboard&state=" + stateOf(url)
		return nil
	}
	if code := h.run("login", "--timeout", "2s"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if h.installs != 1 || h.http.CallsTo(auth.TokenURL)[0].Body.(map[string]string)["code"] != "from-clipboard" {
		t.Errorf("installs %d", h.installs)
	}

	h = newHarness(t)
	h.deps.Handler.Install = func(string) (string, error) { return "", errors.New("nope") }
	h.deps.OpenBrowser = func(url, _ string) error {
		h.clip.text = auth.RedirectURI + "?code=c&state=" + stateOf(url)
		return nil
	}
	if code := h.run("login", "--timeout", "2s"); code != 0 || !strings.Contains(h.stderr.String(), "Could not register") {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
}

func TestLoginTimesOutAndNamedBrowser(t *testing.T) {
	h := newHarness(t)
	h.handler = true
	if code := h.run("login", "--timeout", "0s", "--browser", "firefox"); code != 1 || !strings.Contains(h.stderr.String(), "timed out") {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if !strings.HasPrefix(h.opened[0], "firefox|https://auth.tesla.com/") {
		t.Errorf("opened %v", h.opened)
	}
}

func TestNonMacOSUsesPasteFlow(t *testing.T) {
	h := newHarness(t)
	h.deps.Handler.Supported = func() bool { return false }
	h.deps.OpenBrowser = func(url, _ string) error {
		h.stdin = auth.RedirectURI + "?code=pasted&state=" + stateOf(url) + "\n"
		return nil
	}
	if code := h.run("login"); code != 0 || !strings.Contains(h.stdout.String(), "Callback URL:") {
		t.Fatalf("exit %d: %s", code, h.stdout.String())
	}
	if h.installs != 0 {
		t.Error("helper must not be installed on non-macOS")
	}
}

func TestCheck(t *testing.T) {
	h := newHarness(t)
	if code := h.run("check"); code != 2 {
		t.Fatalf("not logged in: exit %d", code)
	}
	h.loggedIn()
	if code := h.run("check"); code != 0 || !strings.Contains(h.stdout.String(), "RN1") {
		t.Fatalf("exit %d: %s", code, h.stdout.String())
	}
	if code := h.run("check", "-q"); code != 0 || h.stdout.String() != "" || !strings.Contains(h.stderr.String(), "No changes") {
		t.Fatalf("quiet: %q / %q", h.stdout.String(), h.stderr.String())
	}
	h.http.Routes[api.OrdersURL] = testutil.Route{Value: map[string]any{"response": []any{map[string]any{"referenceNumber": "RN1", "orderStatus": "DELIVERED"}}}}
	h.run("check", "-q")
	if out := h.stdout.String(); !strings.Contains(out, "Changes since the last check:") || !strings.Contains(out, `~ order.orderStatus: "BOOKED" -> "DELIVERED"`) {
		t.Fatalf("changes: %s", out)
	}
	if code := h.run("check", "-f", "xml"); code != 1 {
		t.Errorf("bad format: exit %d", code)
	}
}

func TestCheckJSONAndShowFormats(t *testing.T) {
	h := newHarness(t)
	h.loggedIn()
	h.run("check", "-f", "json")
	var doc map[string]any
	if err := json.Unmarshal(h.stdout.Bytes(), &doc); err != nil {
		t.Fatalf("check json: %v\n%s", err, h.stdout.String())
	}
	if code := h.run("show", "-f", "yaml", "--all"); code != 0 || !strings.Contains(h.stdout.String(), "data_fields:") {
		t.Fatalf("show: %s", h.stdout.String())
	}

	entry := testutil.Entry()
	dump, _ := json.Marshal(map[string]any{"orders": []any{entry["order"]}, "details": map[string]any{"RN1": entry["details"]}})
	file := filepath.Join(h.dir, "dump.json")
	_ = os.WriteFile(file, dump, 0o600)
	h.run("show", "-i", file, "-f", "json")
	if err := json.Unmarshal(h.stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	delivery := doc["orders"].([]any)[0].(map[string]any)["delivery"].(map[string]any)
	if delivery["location"] != "Somewhere Center" {
		t.Errorf("show -i: %v", delivery)
	}
	if code := h.run("show", "-i", filepath.Join(h.dir, "missing.json")); code != 1 {
		t.Errorf("missing file: exit %d", code)
	}
}

func TestDump(t *testing.T) {
	h := newHarness(t)
	if code := h.run("dump"); code != 2 {
		t.Fatalf("not logged in: exit %d", code)
	}
	h.loggedIn()
	if code := h.run("dump", "--country", "NL"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	var data map[string]any
	if err := json.Unmarshal(h.stdout.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data["orders"].([]any)[0].(map[string]any)["referenceNumber"] != "RN1" ||
		data["token"].(map[string]any)["claims"].(map[string]any)["exp"] != float64(future) {
		t.Errorf("dump %v", data)
	}
	if !strings.Contains(h.http.CallsTo(api.TasksURL)[0].URL, "deviceCountry=NL") || h.store.LoadSnapshot() != nil {
		t.Error("dump should pass country and save nothing")
	}
	out := filepath.Join(h.dir, "dump.yaml")
	if code := h.run("dump", "-f", "yaml", "-o", out); code != 0 {
		t.Fatalf("yaml: exit %d", code)
	}
	raw, _ := os.ReadFile(out)
	if yaml.Unmarshal(raw, &data) != nil || data["orders"] == nil {
		t.Errorf("yaml dump %s", raw)
	}
}

func TestLogoutAndURLHandler(t *testing.T) {
	h := newHarness(t)
	h.loggedIn()
	if h.run("logout"); h.store.LoadTokens() != nil || !strings.Contains(h.stdout.String(), "Tokens deleted.") {
		t.Error("logout")
	}
	h.handler = true
	if h.run("url-handler", "status"); !strings.Contains(h.stdout.String(), "Helper installed: yes") {
		t.Error(h.stdout.String())
	}
	h.deps.Handler.Supported = func() bool { return false }
	if code := h.run("url-handler", "install"); code != 1 || !strings.Contains(h.stderr.String(), "only available on macOS") {
		t.Errorf("exit %d: %s", code, h.stderr.String())
	}
}

func TestDebugFlagRecordsToDebugDir(t *testing.T) {
	h := newHarness(t)
	h.loggedIn()
	var gotRecorder httpx.Recorder
	h.deps.NewHTTP = func(r httpx.Recorder) (httpx.Client, error) { gotRecorder = r; return h.http, nil }
	debugDir := filepath.Join(h.dir, "d")
	if code := h.run("--debug", "--debug-dir", debugDir, "check", "-q"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if gotRecorder == nil {
		t.Fatal("--debug should pass a recorder")
	}
	if entries, _ := os.ReadDir(debugDir); len(entries) != 1 {
		t.Errorf("expected one timestamped run folder, got %v", entries)
	}
}

func TestOrdersFromFile(t *testing.T) {
	if len(OrdersFromFile(map[string]any{})) != 0 {
		t.Error("empty input")
	}
	snap := map[string]any{"orders": map[string]any{"RN1": map[string]any{}}}
	if _, ok := OrdersFromFile(snap)["RN1"]; !ok {
		t.Error("snapshot format")
	}
}

// macOS may report a freshly installed handler with a delay; login must still
// use the helper's inbox.
func TestLoginUsesInboxRightAfterInstall(t *testing.T) {
	h := newHarness(t)
	h.deps.Handler.Install = func(string) (string, error) { h.installs++; return "app", nil } // Installed stays false
	h.deps.NewClipboard = func() (callback.Source, bool) { return nil, false }
	h.deps.OpenBrowser = func(url, _ string) error {
		return os.WriteFile(urlhandler.InboxPath(h.dir), []byte(auth.RedirectURI+"?code=fresh&state="+stateOf(url)), 0o600)
	}
	if code := h.run("login", "--timeout", "2s"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if h.installs != 1 || h.http.CallsTo(auth.TokenURL)[0].Body.(map[string]string)["code"] != "fresh" {
		t.Error("inbox not used after install")
	}
}
