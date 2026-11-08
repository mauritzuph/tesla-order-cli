// Package httpx is the only place that talks to the network.
//
// Tesla's auth endpoint (behind Akamai) fingerprints the TLS handshake: a token
// obtained through a non-browser handshake is rejected by the owner API with
// "403 forbidden". The client therefore impersonates Chrome's TLS and HTTP/2
// fingerprint via bogdanfinn/tls-client.
package httpx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

const (
	defaultTimeout = 30 * time.Second
	maxTextBody    = 100_000
)

// Error is a failed request: an HTTP error status, or Status 0 for network errors.
type Error struct {
	Status  int
	URL     string
	Message string
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("request failed for %s: %s", e.URL, e.Message)
	}
	return fmt.Sprintf("HTTP %d for %s: %s", e.Status, e.URL, e.Message)
}

// Client is what the rest of the code needs. Responses are raw JSON bodies.
type Client interface {
	PostForm(url string, form map[string]string) ([]byte, error)
	PostJSON(url string, payload any, headers map[string]string) ([]byte, error)
	GetJSON(url string, headers map[string]string) ([]byte, error)
}

// Exchange is one request/response, as seen by a Recorder (debug mode).
type Exchange struct {
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	RequestHeaders  map[string]string `json:"request_headers"`
	RequestBody     any               `json:"request_body"`
	Status          *int              `json:"status"`
	ResponseHeaders map[string]string `json:"response_headers"`
	ResponseBody    any               `json:"response_body"`
	ElapsedMS       float64           `json:"elapsed_ms"`
	Error           *string           `json:"error"`
}

// Recorder receives every exchange; used by --debug.
type Recorder interface {
	Record(Exchange)
}

// TLSClient implements Client with Chrome TLS impersonation.
type TLSClient struct {
	http     tlsclient.HttpClient
	recorder Recorder
}

// New creates a client. recorder may be nil.
func New(recorder Recorder) (*TLSClient, error) {
	c, err := tlsclient.NewHttpClient(
		tlsclient.NewNoopLogger(),
		tlsclient.WithTimeoutSeconds(int(defaultTimeout.Seconds())),
		tlsclient.WithClientProfile(profiles.Chrome_152),
	)
	if err != nil {
		return nil, err
	}
	return &TLSClient{http: c, recorder: recorder}, nil
}

func (c *TLSClient) PostForm(u string, form map[string]string) ([]byte, error) {
	values := url.Values{}
	for k, v := range form {
		values.Set(k, v)
	}
	headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	return c.send("POST", u, headers, []byte(values.Encode()), form)
}

func (c *TLSClient) PostJSON(u string, payload any, headers map[string]string) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	h := map[string]string{"Content-Type": "application/json"}
	for k, v := range headers {
		h[k] = v
	}
	return c.send("POST", u, h, body, payload)
}

func (c *TLSClient) GetJSON(u string, headers map[string]string) ([]byte, error) {
	return c.send("GET", u, headers, nil, nil)
}

func (c *TLSClient) send(method, u string, headers map[string]string, body []byte, logBody any) ([]byte, error) {
	ex := Exchange{Method: method, URL: u, RequestHeaders: copyMap(headers), RequestBody: logBody}
	started := time.Now()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, u, reader)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		msg := err.Error()
		ex.Error = &msg
		c.record(ex, started)
		return nil, &Error{URL: u, Message: msg}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		msg := err.Error()
		ex.Error = &msg
		c.record(ex, started)
		return nil, &Error{Status: resp.StatusCode, URL: u, Message: msg}
	}

	status := resp.StatusCode
	ex.Status = &status
	ex.ResponseHeaders = flattenHeaders(resp.Header)
	ex.ResponseBody = DecodeBody(data)
	c.record(ex, started)

	if status >= 400 {
		return nil, &Error{Status: status, URL: u, Message: truncate(string(data), 300)}
	}
	if len(data) > 0 && !json.Valid(data) {
		return nil, &Error{Status: status, URL: u, Message: "response was not JSON"}
	}
	return data, nil
}

func (c *TLSClient) record(ex Exchange, started time.Time) {
	if c.recorder == nil {
		return
	}
	ex.ElapsedMS = float64(time.Since(started).Microseconds()/100) / 10
	defer func() { _ = recover() }() // debugging must never break the actual request
	c.recorder.Record(ex)
}

// DecodeBody returns parsed JSON, otherwise (truncated) text, nil when empty.
func DecodeBody(data []byte) any {
	if len(data) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(data, &v); err == nil {
		return v
	}
	return truncate(string(data), maxTextBody)
}

func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	return out
}

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
