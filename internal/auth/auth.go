// Package auth implements Tesla's OAuth2 PKCE login and token refresh.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mauritzuph/tesla-order-cli/internal/httpx"
)

const (
	ClientID     = "ownerapi"
	RedirectURI  = "tesla://auth/callback"
	AuthorizeURL = "https://auth.tesla.com/oauth2/v3/authorize"
	TokenURL     = "https://auth.tesla.com/oauth2/v3/token"
	// Read-only: enough for orders. Vehicle scopes would let a stored token
	// command the car, which an order tracker must not be able to do.
	Scope = "openid email offline_access"

	// Refresh a little before the access token actually expires.
	expiryMargin = 5 * time.Minute
)

// ErrLoginRequired means there are no usable tokens; the user must run `login`.
type ErrLoginRequired struct{ Reason string }

func (e *ErrLoginRequired) Error() string { return e.Reason }

// CallbackError is an invalid tesla://auth/callback URL.
type CallbackError struct{ Reason string }

func (e *CallbackError) Error() string { return e.Reason }

// ErrStateMismatch: the callback belongs to another (e.g. earlier) login attempt.
var ErrStateMismatch = &CallbackError{"the URL belongs to a different login attempt (state mismatch)"}

// PKCE holds one login attempt's secrets.
type PKCE struct {
	Verifier  string
	Challenge string
	State     string
}

func NewPKCE() PKCE {
	verifier := b64url(randomBytes(32))
	sum := sha256.Sum256([]byte(verifier))
	return PKCE{Verifier: verifier, Challenge: b64url(sum[:]), State: hex.EncodeToString(randomBytes(16))}
}

func (p PKCE) AuthorizeURL() string {
	q := url.Values{
		"client_id":             {ClientID},
		"redirect_uri":          {RedirectURI},
		"response_type":         {"code"},
		"scope":                 {Scope},
		"state":                 {p.State},
		"code_challenge":        {p.Challenge},
		"code_challenge_method": {"S256"},
	}
	return AuthorizeURL + "?" + q.Encode()
}

func IsCallbackURL(u string) bool { return strings.HasPrefix(u, RedirectURI) }

// ParseCallback returns the authorization code from a tesla://auth/callback URL.
func ParseCallback(raw, expectedState string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !IsCallbackURL(raw) {
		return "", &CallbackError{fmt.Sprintf("expected a URL starting with %q", RedirectURI)}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", &CallbackError{"the URL could not be parsed"}
	}
	q := u.Query()
	if e := q.Get("error"); e != "" {
		detail := q.Get("error_description")
		if detail == "" {
			detail = e
		}
		return "", &CallbackError{"Tesla rejected the login: " + detail}
	}
	if q.Has("state") && q.Get("state") != expectedState {
		return "", ErrStateMismatch
	}
	code := q.Get("code")
	if code == "" {
		return "", &CallbackError{"the URL does not contain an authorization code"}
	}
	return code, nil
}

// Tokens is the stored login. The JSON format is shared with earlier versions.
type Tokens struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresAt    float64 `json:"expires_at"`
}

func (t Tokens) Expired(now time.Time) bool {
	return float64(now.Add(expiryMargin).Unix()) >= t.ExpiresAt
}

// UnmarshalJSON also accepts token files without expires_at (original script).
func (t *Tokens) UnmarshalJSON(data []byte) error {
	var raw struct {
		AccessToken  string   `json:"access_token"`
		RefreshToken string   `json:"refresh_token"`
		ExpiresAt    *float64 `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.AccessToken == "" || raw.RefreshToken == "" {
		return errors.New("token file is missing access or refresh token")
	}
	t.AccessToken, t.RefreshToken = raw.AccessToken, raw.RefreshToken
	if raw.ExpiresAt != nil {
		t.ExpiresAt = *raw.ExpiresAt
	} else if exp, ok := JWTExpiry(raw.AccessToken); ok {
		t.ExpiresAt = exp
	}
	return nil
}

type tokenResponse struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	ExpiresIn    *float64 `json:"expires_in"`
}

// TokensFromResponse builds Tokens from a token endpoint response. Tesla rotates
// refresh tokens: always keep the newest one, and only fall back to the previous
// one if the response has none.
func TokensFromResponse(body []byte, now time.Time, previousRefresh string) (Tokens, error) {
	var r tokenResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return Tokens{}, fmt.Errorf("unexpected token response: %w", err)
	}
	if r.AccessToken == "" {
		return Tokens{}, errors.New("token response did not contain an access token")
	}
	refresh := r.RefreshToken
	if refresh == "" {
		refresh = previousRefresh
	}
	if refresh == "" {
		return Tokens{}, errors.New("token response did not contain a refresh token")
	}
	expires := float64(now.Unix())
	if r.ExpiresIn != nil {
		expires += *r.ExpiresIn
	} else if exp, ok := JWTExpiry(r.AccessToken); ok {
		expires = exp
	}
	return Tokens{AccessToken: r.AccessToken, RefreshToken: refresh, ExpiresAt: expires}, nil
}

// JWTClaims decodes a JWT payload without verifying it.
func JWTClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(data, &claims) != nil {
		return nil
	}
	return claims
}

func JWTExpiry(token string) (float64, bool) {
	exp, ok := JWTClaims(token)["exp"].(float64)
	return exp, ok
}

// Client talks to Tesla's token endpoint.
type Client struct {
	HTTP httpx.Client
	Now  func() time.Time
}

func (c Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c Client) ExchangeCode(code string, p PKCE) (Tokens, error) {
	body, err := c.HTTP.PostForm(TokenURL, map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     ClientID,
		"code":          code,
		"redirect_uri":  RedirectURI,
		"code_verifier": p.Verifier,
	})
	if err != nil {
		return Tokens{}, err
	}
	return TokensFromResponse(body, c.now(), "")
}

func (c Client) Refresh(t Tokens) (Tokens, error) {
	body, err := c.HTTP.PostForm(TokenURL, map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     ClientID,
		"refresh_token": t.RefreshToken,
	})
	if err != nil {
		var he *httpx.Error
		if errors.As(err, &he) && (he.Status == 400 || he.Status == 401 || he.Status == 403) {
			return Tokens{}, &ErrLoginRequired{"Tesla rejected the refresh token; please log in again."}
		}
		return Tokens{}, err
	}
	return TokensFromResponse(body, c.now(), t.RefreshToken)
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
