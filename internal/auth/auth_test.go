package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const testSessionSecret = "0123456789abcdef0123456789abcdef"

type mockProvider struct {
	server          *httptest.Server
	key             *rsa.PrivateKey
	nonce           string
	codeChallenge   string
	groups          []string
	userInfoSubject string
}

func newMockProvider(t *testing.T) *mockProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	provider := &mockProvider{key: key, groups: []string{"registry-admins"}, userInfoSubject: "user-123"}
	provider.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(w, map[string]any{
				"issuer":                                provider.server.URL,
				"authorization_endpoint":                provider.server.URL + "/authorize",
				"token_endpoint":                        provider.server.URL + "/token",
				"userinfo_endpoint":                     provider.server.URL + "/userinfo",
				"jwks_uri":                              provider.server.URL + "/jwks",
				"response_types_supported":              []string{"code"},
				"subject_types_supported":               []string{"public"},
				"id_token_signing_alg_values_supported": []string{"RS256"},
				"token_endpoint_auth_methods_supported": []string{"client_secret_basic"},
			})
		case "/jwks":
			writeTestJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
				Key:       &provider.key.PublicKey,
				KeyID:     "test-key",
				Algorithm: string(jose.RS256),
				Use:       "sig",
			}}})
		case "/token":
			provider.token(w, r)
		case "/userinfo":
			provider.userInfo(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.server.Close)
	return provider
}

func (p *mockProvider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || r.FormValue("code") != "valid-code" {
		http.Error(w, "invalid code", http.StatusBadRequest)
		return
	}
	verifierHash := sha256.Sum256([]byte(r.FormValue("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(verifierHash[:]) != p.codeChallenge {
		http.Error(w, "invalid verifier", http.StatusBadRequest)
		return
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: p.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key"),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now()
	idToken, err := jwt.Signed(signer).
		Claims(jwt.Claims{
			Issuer:   p.server.URL,
			Subject:  "user-123",
			Audience: jwt.Audience{"mini-harbor"},
			Expiry:   jwt.NewNumericDate(now.Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(now),
		}).
		Claims(map[string]any{
			"nonce": p.nonce,
		}).
		Serialize()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeTestJSON(w, map[string]any{
		"access_token": "access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     idToken,
	})
}

func (p *mockProvider) userInfo(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer access-token" {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	writeTestJSON(w, map[string]any{
		"sub":                p.userInfoSubject,
		"name":               "Ada Lovelace",
		"email":              "ada@example.test",
		"preferred_username": "ada",
		"groups":             p.groups,
	})
}

func TestAuthorizationCodeFlowCreatesSession(t *testing.T) {
	provider := newMockProvider(t)
	authentication := newTestAuthenticator(t, provider)
	mux := http.NewServeMux()
	authentication.Register(mux)
	mux.HandleFunc("GET /api/auth/me", authentication.Status)

	login := httptest.NewRecorder()
	mux.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/auth/login?return=%2Fimages%3Ftag%3Dlatest", nil))
	if login.Code != http.StatusFound {
		t.Fatalf("login status = %d, body = %s", login.Code, login.Body.String())
	}
	authorizationURL, err := url.Parse(login.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	query := authorizationURL.Query()
	if query.Get("code_challenge_method") != "S256" || query.Get("state") == "" || query.Get("nonce") == "" {
		t.Fatalf("invalid authorization parameters: %s", authorizationURL.RawQuery)
	}
	provider.nonce = query.Get("nonce")
	provider.codeChallenge = query.Get("code_challenge")
	stateCookie := responseCookie(t, login, loginCookieName)

	callback := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/auth/callback?code=valid-code&state="+url.QueryEscape(query.Get("state")), nil)
	request.AddCookie(stateCookie)
	mux.ServeHTTP(callback, request)
	if callback.Code != http.StatusFound || callback.Header().Get("Location") != "/images?tag=latest" {
		t.Fatalf("callback status = %d, location = %q, body = %s", callback.Code, callback.Header().Get("Location"), callback.Body.String())
	}
	sessionCookie := responseCookie(t, callback, sessionCookieName)
	if !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie is missing security attributes: %#v", sessionCookie)
	}

	status := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	request.AddCookie(sessionCookie)
	mux.ServeHTTP(status, request)
	if status.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status.Code, status.Body.String())
	}
	var identity struct {
		Enabled       bool `json:"enabled"`
		Authenticated bool `json:"authenticated"`
		User          User `json:"user"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &identity); err != nil {
		t.Fatal(err)
	}
	if !identity.Enabled || !identity.Authenticated || identity.User.Name != "Ada Lovelace" || identity.User.Subject != "user-123" {
		t.Fatalf("unexpected identity: %#v", identity)
	}
}

func TestMiddlewareAndLogout(t *testing.T) {
	provider := newMockProvider(t)
	authentication := newTestAuthenticator(t, provider)
	protected := authentication.RequireAPI(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	response := httptest.NewRecorder()
	protected.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("anonymous read status = %d", response.Code)
	}

	response = httptest.NewRecorder()
	protected.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/organizations", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated write status = %d", response.Code)
	}

	sessionValue, err := authentication.codec.seal(session{
		User:      User{Subject: "user-123", Name: "Ada Lovelace"},
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/organizations", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionValue})
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("authenticated write status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodDelete, "http://mini-harbor.test/api/repositories/team/app/tags/latest", nil)
	request.Header.Set("Origin", "https://attacker.example")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionValue})
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin API status = %d", response.Code)
	}

	mux := http.NewServeMux()
	authentication.Register(mux)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/auth/logout", nil))
	deleted := responseCookie(t, response, sessionCookieName)
	if response.Code != http.StatusNoContent || deleted.MaxAge != -1 {
		t.Fatalf("logout status = %d, cookie = %#v", response.Code, deleted)
	}
}

func TestCallbackRejectsUnauthorizedGroup(t *testing.T) {
	provider := newMockProvider(t)
	provider.groups = []string{"developers"}
	authentication := newTestAuthenticator(t, provider)
	mux := http.NewServeMux()
	authentication.Register(mux)

	login := httptest.NewRecorder()
	mux.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	authorizationURL, err := url.Parse(login.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	provider.nonce = authorizationURL.Query().Get("nonce")
	provider.codeChallenge = authorizationURL.Query().Get("code_challenge")

	callback := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/auth/callback?code=valid-code&state="+url.QueryEscape(authorizationURL.Query().Get("state")), nil)
	request.AddCookie(responseCookie(t, login, loginCookieName))
	mux.ServeHTTP(callback, request)
	if callback.Code != http.StatusForbidden {
		t.Fatalf("callback status = %d, body = %s", callback.Code, callback.Body.String())
	}
}

func TestCallbackRejectsUserInfoSubjectMismatch(t *testing.T) {
	provider := newMockProvider(t)
	provider.userInfoSubject = "other-user"
	authentication := newTestAuthenticator(t, provider)
	mux := http.NewServeMux()
	authentication.Register(mux)

	login := httptest.NewRecorder()
	mux.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	authorizationURL, err := url.Parse(login.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	provider.nonce = authorizationURL.Query().Get("nonce")
	provider.codeChallenge = authorizationURL.Query().Get("code_challenge")

	callback := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/auth/callback?code=valid-code&state="+url.QueryEscape(authorizationURL.Query().Get("state")), nil)
	request.AddCookie(responseCookie(t, login, loginCookieName))
	mux.ServeHTTP(callback, request)
	if callback.Code != http.StatusUnauthorized {
		t.Fatalf("callback status = %d, body = %s", callback.Code, callback.Body.String())
	}
}

func TestLoginRejectsExternalReturnURL(t *testing.T) {
	provider := newMockProvider(t)
	authentication := newTestAuthenticator(t, provider)
	mux := http.NewServeMux()
	authentication.Register(mux)

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/login?return=%2F%5Cevil.example", nil))
	var state loginState
	if err := authentication.codec.open(responseCookie(t, response, loginCookieName).Value, &state); err != nil {
		t.Fatal(err)
	}
	if state.ReturnPath != "/" {
		t.Fatalf("unsafe return path accepted: %q", state.ReturnPath)
	}
}

func newTestAuthenticator(t *testing.T, provider *mockProvider) *Authenticator {
	t.Helper()
	authentication, err := New(context.Background(), Config{
		Issuer:        provider.server.URL,
		ClientID:      "mini-harbor",
		ClientSecret:  "client-secret",
		RedirectURL:   "http://mini-harbor.test/auth/callback",
		AllowedGroups: []string{"registry-admins"},
		SessionSecret: []byte(testSessionSecret),
	})
	if err != nil {
		t.Fatal(err)
	}
	return authentication
}

func responseCookie(t *testing.T, response *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %q not found", name)
	return nil
}

func writeTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
