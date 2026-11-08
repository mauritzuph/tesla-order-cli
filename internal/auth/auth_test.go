package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mauritzuph/tesla-order-cli/internal/httpx"
	"github.com/mauritzuph/tesla-order-cli/internal/testutil"
)

var now = time.Unix(1_800_000_000, 0)

func TestPKCEChallengeMatchesVerifier(t *testing.T) {
	p := NewPKCE()
	sum := sha256.Sum256([]byte(p.Verifier))
	if p.Challenge != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Fatal("challenge does not match verifier")
	}
	if NewPKCE().State == p.State {
		t.Fatal("state should be random")
	}
}

func TestAuthorizeURL(t *testing.T) {
	p := NewPKCE()
	u, _ := url.Parse(p.AuthorizeURL())
	q := u.Query()
	for k, want := range map[string]string{
		"code_challenge": p.Challenge, "code_challenge_method": "S256", "state": p.State,
		"redirect_uri": RedirectURI, "scope": "openid email offline_access",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
}

func TestParseCallback(t *testing.T) {
	code, err := ParseCallback(RedirectURI+"?code=abc123&state=s1&issuer=x", "s1")
	if err != nil || code != "abc123" {
		t.Fatalf("got %q, %v", code, err)
	}
	code, err = ParseCallback("  "+RedirectURI+"?code=abc \n", "s1")
	if err != nil || code != "abc" {
		t.Fatalf("whitespace/missing state: got %q, %v", code, err)
	}
	for _, tc := range []struct{ url, msg string }{
		{"https://example.com/?code=abc", "expected a URL"},
		{RedirectURI + "?code=abc&state=other", "state mismatch"},
		{RedirectURI + "?state=s1", "authorization code"},
		{RedirectURI + "?error=access_denied&error_description=Nope", "Nope"},
	} {
		_, err := ParseCallback(tc.url, "s1")
		if err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%s: err = %v, want %q", tc.url, err, tc.msg)
		}
	}
	if _, err := ParseCallback(RedirectURI+"?code=a&state=x", "s1"); !errors.Is(err, ErrStateMismatch) {
		t.Error("state mismatch should be ErrStateMismatch")
	}
}

func TestTokensFromResponse(t *testing.T) {
	tok, err := TokensFromResponse([]byte(`{"access_token":"a","refresh_token":"r","expires_in":3600}`), now, "")
	if err != nil || tok != (Tokens{"a", "r", 1_800_003_600}) {
		t.Fatalf("got %+v, %v", tok, err)
	}
	access := testutil.JWT(1_800_000_100)
	tok, _ = TokensFromResponse([]byte(`{"access_token":"`+access+`","refresh_token":"r"}`), now, "")
	if tok.ExpiresAt != 1_800_000_100 {
		t.Errorf("JWT fallback: %v", tok.ExpiresAt)
	}
	tok, _ = TokensFromResponse([]byte(`{"access_token":"a","expires_in":1}`), now, "old")
	if tok.RefreshToken != "old" {
		t.Errorf("should keep previous refresh token, got %q", tok.RefreshToken)
	}
	if _, err := TokensFromResponse([]byte(`{"access_token":"a","expires_in":1}`), now, ""); err == nil {
		t.Error("missing refresh token should fail")
	}
}

func TestTokensExpiryMarginAndLegacyFormat(t *testing.T) {
	tok := Tokens{"a", "r", 1_800_000_200}
	if !tok.Expired(now) {
		t.Error("within the 5 minute margin should count as expired")
	}
	var legacy Tokens
	data := `{"access_token":"` + testutil.JWT(1_800_000_005) + `","refresh_token":"r","id_token":"x"}`
	if err := json.Unmarshal([]byte(data), &legacy); err != nil || legacy.ExpiresAt != 1_800_000_005 {
		t.Errorf("legacy token file: %+v, %v", legacy, err)
	}
	if err := json.Unmarshal([]byte(`{"access_token":"a"}`), &legacy); err == nil {
		t.Error("token file without refresh token should be rejected")
	}
}

func TestJWTClaimsHandlesGarbage(t *testing.T) {
	if JWTClaims("not-a-jwt") != nil || JWTClaims("a.!!!.c") != nil {
		t.Error("garbage should give nil claims")
	}
}

func TestExchangeCodeSendsVerifier(t *testing.T) {
	http := testutil.NewFakeHTTP(map[string]testutil.Route{
		TokenURL: {Value: map[string]any{"access_token": "a", "refresh_token": "r", "expires_in": 60}},
	})
	p := NewPKCE()
	tok, err := Client{HTTP: http, Now: func() time.Time { return now }}.ExchangeCode("code1", p)
	if err != nil || tok != (Tokens{"a", "r", 1_800_000_060}) {
		t.Fatalf("got %+v, %v", tok, err)
	}
	body := http.Calls[0].Body.(map[string]string)
	if body["grant_type"] != "authorization_code" || body["code"] != "code1" || body["code_verifier"] != p.Verifier {
		t.Errorf("bad body %v", body)
	}
}

func TestRefreshReturnsRotatedToken(t *testing.T) {
	http := testutil.NewFakeHTTP(map[string]testutil.Route{
		TokenURL: {Value: map[string]any{"access_token": "a2", "refresh_token": "r2", "expires_in": 60}},
	})
	tok, err := Client{HTTP: http, Now: func() time.Time { return now }}.Refresh(Tokens{"a1", "r1", 0})
	if err != nil || tok != (Tokens{"a2", "r2", 1_800_000_060}) {
		t.Fatalf("got %+v, %v", tok, err)
	}
	body := http.Calls[0].Body.(map[string]string)
	if body["refresh_token"] != "r1" || body["grant_type"] != "refresh_token" || body["client_id"] != "ownerapi" {
		t.Errorf("bad body %v", body)
	}
}

func TestRejectedRefreshMeansLoginRequired(t *testing.T) {
	for _, status := range []int{400, 401, 403} {
		http := testutil.NewFakeHTTP(map[string]testutil.Route{TokenURL: {Err: &httpx.Error{Status: status}}})
		_, err := Client{HTTP: http}.Refresh(Tokens{"a", "r", 0})
		var login *ErrLoginRequired
		if !errors.As(err, &login) {
			t.Errorf("status %d: got %v", status, err)
		}
	}
	http := testutil.NewFakeHTTP(map[string]testutil.Route{TokenURL: {Err: &httpx.Error{Status: 503}}})
	_, err := Client{HTTP: http}.Refresh(Tokens{"a", "r", 0})
	var login *ErrLoginRequired
	if errors.As(err, &login) || err == nil {
		t.Errorf("503 should be a plain error, got %v", err)
	}
}
