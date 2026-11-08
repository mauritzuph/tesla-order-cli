// Package debug implements --debug: dump every API request and response.
//
// Each exchange is printed in full to stderr and written as one JSON file to
// <data dir>/debug/<run timestamp>/. Tokens, auth codes and similar secrets are
// always redacted. Order data (name, address, VIN, ...) is not.
package debug

import (
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mauritzuph/tesla-order-cli/internal/httpx"
	"github.com/mauritzuph/tesla-order-cli/internal/ordered"
)

var secretKeys = map[string]bool{
	"access_token": true, "refresh_token": true, "id_token": true, "code": true,
	"code_verifier": true, "authorization": true, "cookie": true, "set-cookie": true,
	"password": true, "client_secret": true,
}

func mask(v any) string { return fmt.Sprintf("<redacted %d chars>", len(fmt.Sprint(v))) }

func isSecret(k string) bool { return secretKeys[strings.ToLower(k)] }

// Redact recursively replaces values of secret-looking keys.
func Redact(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if isSecret(k) && val != nil && val != "" {
				out[k] = mask(val)
			} else {
				out[k] = Redact(val)
			}
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(x))
		for k, val := range x {
			if isSecret(k) && val != "" {
				out[k] = mask(val)
			} else {
				out[k] = val
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = Redact(e)
		}
		return out
	}
	return v
}

// RedactURL masks secret query parameters.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery == "" {
		return raw
	}
	q := u.Query()
	for k, vals := range q {
		if isSecret(k) {
			for i := range vals {
				vals[i] = mask(vals[i])
			}
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// RedactExchange returns a copy safe to print and store.
func RedactExchange(ex httpx.Exchange) httpx.Exchange {
	ex.URL = RedactURL(ex.URL)
	ex.RequestHeaders, _ = Redact(ex.RequestHeaders).(map[string]string)
	ex.ResponseHeaders, _ = Redact(ex.ResponseHeaders).(map[string]string)
	ex.RequestBody = Redact(ex.RequestBody)
	ex.ResponseBody = Redact(ex.ResponseBody)
	return ex
}

// Recorder prints exchanges and writes them to Dir (if set).
type Recorder struct {
	Dir   string
	Out   io.Writer
	mu    sync.Mutex
	count int
}

// ForRun creates a recorder writing to a fresh timestamped folder under base.
func ForRun(base string, out io.Writer) (*Recorder, error) {
	dir := filepath.Join(base, time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Recorder{Dir: dir, Out: out}, nil
}

func (r *Recorder) Record(ex httpx.Exchange) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count++
	safe := RedactExchange(ex)
	data, err := ordered.MarshalIndent(safe)
	if err != nil {
		slog.Warn("debug: cannot encode exchange", "err", err)
		return
	}
	status := "ERROR"
	if ex.Status != nil {
		status = fmt.Sprint(*ex.Status)
	} else if ex.Error != nil {
		status = "ERROR " + *ex.Error
	}
	if r.Out != nil {
		fmt.Fprintf(r.Out, "\n=== [%d] %s %s -> %s (%v ms) ===\n%s", r.count, ex.Method, safe.URL, status, ex.ElapsedMS, data)
	}
	if r.Dir != "" {
		path := filepath.Join(r.Dir, fmt.Sprintf("%02d-%s-%s.json", r.count, ex.Method, slug(ex.URL)))
		if err := os.WriteFile(path, data, 0o600); err != nil {
			slog.Warn("debug: cannot write dump", "path", path, "err", err)
		}
	}
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]+`)

func slug(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "request"
	}
	s := strings.Trim(nonAlnum.ReplaceAllString(u.Host+u.Path, "_"), "_")
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}
