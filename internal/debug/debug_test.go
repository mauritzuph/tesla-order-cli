package debug

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mauritzuph/tesla-order-cli/internal/httpx"
)

var secrets = []string{"SECRET-ACCESS", "SECRET-REFRESH", "SECRET-CODE", "SECRET-VERIFIER", "SECRET-ID"}

func exchange() httpx.Exchange {
	status := 200
	return httpx.Exchange{
		Method:          "POST",
		URL:             "https://auth.tesla.com/oauth2/v3/token?code=SECRET-CODE&x=1",
		RequestHeaders:  map[string]string{"Authorization": "Bearer SECRET-ACCESS", "Accept": "json"},
		RequestBody:     map[string]any{"code": "SECRET-CODE", "code_verifier": "SECRET-VERIFIER", "grant": "a"},
		Status:          &status,
		ResponseHeaders: map[string]string{"set-cookie": "ak=1", "content-type": "application/json"},
		ResponseBody: map[string]any{
			"access_token": "SECRET-ACCESS", "refresh_token": "SECRET-REFRESH",
			"nested": []any{map[string]any{"id_token": "SECRET-ID", "expires_in": 28800.0}},
		},
		ElapsedMS: 12.5,
	}
}

func TestRedact(t *testing.T) {
	out := Redact(map[string]any{"Authorization": "Bearer abc", "list": []any{map[string]any{"refresh_token": "r"}}, "ok": 1.0}).(map[string]any)
	if out["Authorization"] != "<redacted 10 chars>" || out["ok"] != 1.0 {
		t.Errorf("got %v", out)
	}
	if nested := out["list"].([]any)[0].(map[string]any)["refresh_token"]; nested != "<redacted 1 chars>" {
		t.Errorf("nested %v", nested)
	}
	if got := Redact(map[string]any{"code": nil}).(map[string]any)["code"]; got != nil {
		t.Errorf("nothing to hide, got %v", got)
	}
	if u := RedactURL("https://x/cb?code=abc&state=s"); strings.Contains(u, "abc") || !strings.Contains(u, "state=s") {
		t.Errorf("url %s", u)
	}
}

func TestRecorderPrintsAndWritesRedactedFiles(t *testing.T) {
	var out bytes.Buffer
	dir := filepath.Join(t.TempDir(), "dumps")
	_ = os.MkdirAll(dir, 0o700)
	r := &Recorder{Dir: dir, Out: &out}
	r.Record(exchange())
	ex := exchange()
	ex.Method, ex.URL = "GET", "https://owner-api.teslamotors.com/api/1/users/orders"
	r.Record(ex)

	if !strings.Contains(out.String(), "[1] POST https://auth.tesla.com/oauth2/v3/token") || !strings.Contains(out.String(), "-> 200 (12.5 ms)") {
		t.Errorf("stderr output: %s", out.String())
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		for _, s := range secrets {
			if strings.Contains(string(data), s) {
				t.Errorf("%s leaked into %s", s, e.Name())
			}
		}
		if info, _ := e.Info(); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", info.Mode().Perm())
		}
		if !strings.Contains(string(data), "28800") {
			t.Error("non-secret values must be kept")
		}
	}
	want := []string{"01-POST-auth_tesla_com_oauth2_v3_token.json", "02-GET-owner_api_teslamotors_com_api_1_users_orders.json"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("files %v", names)
	}
	for _, s := range secrets {
		if strings.Contains(out.String(), s) {
			t.Errorf("%s leaked to stderr", s)
		}
	}
}

func TestRecorderNetworkError(t *testing.T) {
	var out bytes.Buffer
	msg := "timeout"
	(&Recorder{Out: &out}).Record(httpx.Exchange{Method: "GET", URL: "https://x", Error: &msg})
	if !strings.Contains(out.String(), "-> ERROR timeout") {
		t.Error(out.String())
	}
}

func TestForRunCreatesTimestampedFolder(t *testing.T) {
	base := t.TempDir()
	r, err := ForRun(base, nil)
	if err != nil || filepath.Dir(r.Dir) != base {
		t.Fatalf("%v %v", r, err)
	}
}
