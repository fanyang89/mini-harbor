package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	loginCookieName   = "mini_harbor_oidc"
	sessionCookieName = "mini_harbor_session"
	loginLifetime     = 10 * time.Minute
	defaultSessionTTL = 8 * time.Hour
)

type Config struct {
	Issuer          string
	ClientID        string
	ClientSecret    string
	RedirectURL     string
	Scopes          []string
	GroupsClaim     string
	AllowedGroups   []string
	SessionSecret   []byte
	SessionDuration time.Duration
	CookieSecure    bool
}

type User struct {
	Subject  string `json:"subject"`
	Name     string `json:"name"`
	Email    string `json:"email,omitempty"`
	Username string `json:"username,omitempty"`
}

type Authenticator struct {
	enabled         bool
	oauth2Config    oauth2.Config
	provider        *oidc.Provider
	verifier        *oidc.IDTokenVerifier
	codec           *cookieCodec
	groupsClaim     string
	allowedGroups   map[string]struct{}
	sessionDuration time.Duration
	cookieSecure    bool
	now             func() time.Time
}

type loginState struct {
	State      string `json:"state"`
	Nonce      string `json:"nonce"`
	Verifier   string `json:"verifier"`
	ReturnPath string `json:"returnPath"`
	ExpiresAt  int64  `json:"expiresAt"`
}

type session struct {
	User      User  `json:"user"`
	ExpiresAt int64 `json:"expiresAt"`
}

func Disabled() *Authenticator {
	return &Authenticator{now: time.Now}
}

func New(ctx context.Context, config Config) (*Authenticator, error) {
	if config.Issuer == "" {
		return Disabled(), nil
	}
	if config.ClientID == "" || config.RedirectURL == "" {
		return nil, errors.New("OIDC_CLIENT_ID and OIDC_REDIRECT_URL are required when OIDC is enabled")
	}
	redirectURL, err := url.Parse(config.RedirectURL)
	if err != nil || redirectURL.Host == "" || (redirectURL.Scheme != "http" && redirectURL.Scheme != "https") || redirectURL.Fragment != "" {
		return nil, errors.New("OIDC_REDIRECT_URL must be an absolute HTTP(S) URL without a fragment")
	}
	if len(config.SessionSecret) < 32 {
		return nil, errors.New("OIDC_SESSION_SECRET must be at least 32 bytes")
	}

	provider, err := oidc.NewProvider(ctx, config.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider: %w", err)
	}
	scopes := config.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	} else if !slices.Contains(scopes, oidc.ScopeOpenID) {
		scopes = append([]string{oidc.ScopeOpenID}, scopes...)
	}
	groupsClaim := config.GroupsClaim
	if groupsClaim == "" {
		groupsClaim = "groups"
	}
	sessionDuration := config.SessionDuration
	if sessionDuration <= 0 {
		sessionDuration = defaultSessionTTL
	}
	allowedGroups := make(map[string]struct{}, len(config.AllowedGroups))
	for _, group := range config.AllowedGroups {
		allowedGroups[group] = struct{}{}
	}

	return &Authenticator{
		enabled: true,
		oauth2Config: oauth2.Config{
			ClientID:     config.ClientID,
			ClientSecret: config.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  config.RedirectURL,
			Scopes:       scopes,
		},
		provider:        provider,
		verifier:        provider.Verifier(&oidc.Config{ClientID: config.ClientID}),
		codec:           newCookieCodec(config.SessionSecret),
		groupsClaim:     groupsClaim,
		allowedGroups:   allowedGroups,
		sessionDuration: sessionDuration,
		cookieSecure:    config.CookieSecure,
		now:             time.Now,
	}, nil
}

func (a *Authenticator) Enabled() bool {
	return a != nil && a.enabled
}

func (a *Authenticator) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login", a.login)
	mux.HandleFunc("GET /auth/callback", a.callback)
	mux.HandleFunc("POST /auth/logout", a.logout)
}

func (a *Authenticator) Status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.Enabled() {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "authenticated": true})
		return
	}
	user, err := a.user(r)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "authenticated": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "authenticated": true, "user": user})
}

func (a *Authenticator) RequireAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.Enabled() || r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		if _, err := a.user(r); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Authentication required"})
			return
		}
		if !sameOrigin(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Cross-origin request rejected"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Authenticator) login(w http.ResponseWriter, r *http.Request) {
	if !a.Enabled() {
		http.NotFound(w, r)
		return
	}
	state := oauth2.GenerateVerifier()
	nonce := oauth2.GenerateVerifier()
	verifier := oauth2.GenerateVerifier()
	value, err := a.codec.seal(loginState{
		State:      state,
		Nonce:      nonce,
		Verifier:   verifier,
		ReturnPath: safeReturnPath(r.URL.Query().Get("return")),
		ExpiresAt:  a.now().Add(loginLifetime).Unix(),
	})
	if err != nil {
		http.Error(w, "Unable to start login", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     loginCookieName,
		Value:    value,
		Path:     "/auth/callback",
		MaxAge:   int(loginLifetime.Seconds()),
		HttpOnly: true,
		Secure:   a.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	authorizationURL := a.oauth2Config.AuthCodeURL(
		state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("nonce", nonce),
	)
	http.Redirect(w, r, authorizationURL, http.StatusFound)
}

func (a *Authenticator) callback(w http.ResponseWriter, r *http.Request) {
	if !a.Enabled() {
		http.NotFound(w, r)
		return
	}
	a.clearCookie(w, loginCookieName, "/auth/callback")
	if r.URL.Query().Get("error") != "" {
		http.Error(w, "Identity provider rejected login", http.StatusBadRequest)
		return
	}
	cookie, err := r.Cookie(loginCookieName)
	if err != nil {
		http.Error(w, "Login state is missing or expired", http.StatusBadRequest)
		return
	}
	var state loginState
	if err := a.codec.open(cookie.Value, &state); err != nil || state.ExpiresAt < a.now().Unix() {
		http.Error(w, "Login state is missing or expired", http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(state.State), []byte(r.URL.Query().Get("state"))) != 1 {
		http.Error(w, "Invalid login state", http.StatusBadRequest)
		return
	}
	token, err := a.oauth2Config.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(state.Verifier))
	if err != nil {
		slog.Warn("OIDC code exchange failed", "error", err)
		http.Error(w, "Unable to complete login", http.StatusBadGateway)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "Identity provider did not return an ID token", http.StatusBadGateway)
		return
	}
	idToken, err := a.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		slog.Warn("OIDC ID token verification failed", "error", err)
		http.Error(w, "Unable to verify identity", http.StatusUnauthorized)
		return
	}
	var claims map[string]json.RawMessage
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "Unable to read identity", http.StatusUnauthorized)
		return
	}
	if claimString(claims, "nonce") != state.Nonce {
		http.Error(w, "Invalid identity nonce", http.StatusUnauthorized)
		return
	}
	userInfo, err := a.provider.UserInfo(r.Context(), oauth2.StaticTokenSource(token))
	if err != nil {
		slog.Warn("OIDC UserInfo request failed; using ID token claims", "error", err)
	} else {
		if userInfo.Subject != idToken.Subject {
			http.Error(w, "Identity subject mismatch", http.StatusUnauthorized)
			return
		}
		var userInfoClaims map[string]json.RawMessage
		if err := userInfo.Claims(&userInfoClaims); err != nil {
			http.Error(w, "Unable to read identity", http.StatusUnauthorized)
			return
		}
		for _, name := range []string{"name", "email", "preferred_username", a.groupsClaim} {
			if value, ok := userInfoClaims[name]; ok {
				claims[name] = value
			}
		}
	}
	if !a.groupAllowed(claims) {
		http.Error(w, "Your account is not authorized", http.StatusForbidden)
		return
	}
	user := User{
		Subject:  idToken.Subject,
		Name:     claimString(claims, "name"),
		Email:    claimString(claims, "email"),
		Username: claimString(claims, "preferred_username"),
	}
	if user.Name == "" {
		user.Name = firstNonEmpty(user.Username, user.Email, user.Subject)
	}
	sessionValue, err := a.codec.seal(session{User: user, ExpiresAt: a.now().Add(a.sessionDuration).Unix()})
	if err != nil {
		http.Error(w, "Unable to create session", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionValue,
		Path:     "/",
		Expires:  a.now().Add(a.sessionDuration),
		MaxAge:   int(a.sessionDuration.Seconds()),
		HttpOnly: true,
		Secure:   a.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, state.ReturnPath, http.StatusFound)
}

func (a *Authenticator) logout(w http.ResponseWriter, r *http.Request) {
	if !a.Enabled() {
		http.NotFound(w, r)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "Cross-origin request rejected", http.StatusForbidden)
		return
	}
	a.clearCookie(w, sessionCookieName, "/")
	w.WriteHeader(http.StatusNoContent)
}

func (a *Authenticator) user(r *http.Request) (User, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return User{}, err
	}
	var value session
	if err := a.codec.open(cookie.Value, &value); err != nil {
		return User{}, err
	}
	if value.ExpiresAt < a.now().Unix() || value.User.Subject == "" {
		return User{}, errors.New("session expired")
	}
	return value.User, nil
}

func (a *Authenticator) groupAllowed(claims map[string]json.RawMessage) bool {
	if len(a.allowedGroups) == 0 {
		return true
	}
	var groups []string
	raw, ok := claims[a.groupsClaim]
	if !ok || json.Unmarshal(raw, &groups) != nil {
		return false
	}
	for _, group := range groups {
		if _, ok := a.allowedGroups[group]; ok {
			return true
		}
	}
	return false
}

func (a *Authenticator) clearCookie(w http.ResponseWriter, name, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Path:     path,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: true,
		Secure:   a.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func safeReturnPath(value string) string {
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.Contains(value, "\\") {
		return "/"
	}
	return value
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme != "" && strings.EqualFold(parsed.Host, r.Host)
}

func claimString(claims map[string]json.RawMessage, name string) string {
	var value string
	_ = json.Unmarshal(claims[name], &value)
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "Unknown user"
}

type cookieCodec struct {
	aead cipher.AEAD
}

func newCookieCodec(secret []byte) *cookieCodec {
	key := sha256.Sum256(secret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return &cookieCodec{aead: aead}
}

func (c *cookieCodec) seal(value any) (string, error) {
	plaintext, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *cookieCodec) open(value string, target any) error {
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(sealed) < c.aead.NonceSize() {
		return errors.New("invalid cookie")
	}
	nonce := sealed[:c.aead.NonceSize()]
	plaintext, err := c.aead.Open(nil, nonce, sealed[c.aead.NonceSize():], nil)
	if err != nil {
		return errors.New("invalid cookie")
	}
	return json.Unmarshal(plaintext, target)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
