// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockoidc

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const defaultTenantID = "11111111-2222-3333-4444-555555555555"

const (
	headerContentType = "Content-Type"
	contentTypeJSON   = "application/json"
)

const (
	authCodeTTL     = 10 * time.Minute
	tokenIdleTTL    = 2 * time.Hour
	janitorInterval = 5 * time.Minute
)

const pickerUserLimit = 50

type MockOIDCProviderConfig struct {
	Issuer              string
	ClientID            string
	ClientSecret        string
	PublicClient        bool
	Users               []FakeOIDCUser
	ListenAddr          string
	EnablePicker        bool
	IDTokenOnlyClaims   []string
	TenantID            string
	Logger              *slog.Logger
	AccessTokenClaims   func(sub, grantType string) map[string]any
	RotateRefreshTokens bool
}

type FakeOIDCUser struct {
	Sub    string
	Email  string
	Name   string
	Claims map[string]any
}

type oidcAuthCodeEntry struct {
	UserSub       string
	RedirectURI   string
	Used          bool
	CodeChallenge string
	CodeMethod    string
	Nonce         string
	createdAt     time.Time
}

type oidcAccessTokenEntry struct {
	UserSub  string
	Claims   map[string]any
	lastUsed time.Time
}

type oidcRefreshTokenEntry struct {
	UserSub  string
	lastUsed time.Time
}

type MockOIDCProvider struct {
	server     *http.Server
	config     MockOIDCProviderConfig
	issuer     string
	listenAddr string

	signingKey *rsa.PrivateKey
	kid        string
	tenantID   string

	logger *slog.Logger

	idTokenOnly map[string]struct{}
	usersBySub  map[string]*FakeOIDCUser

	authorizeCount atomic.Int64
	tokenCount     atomic.Int64
	userinfoCount  atomic.Int64

	janitorCancel context.CancelFunc

	mu            sync.RWMutex
	authCodes     map[string]*oidcAuthCodeEntry
	accessTokens  map[string]*oidcAccessTokenEntry
	refreshTokens map[string]*oidcRefreshTokenEntry
}

func NewMockOIDCProvider(cfg MockOIDCProviderConfig) (*MockOIDCProvider, error) {
	if cfg.ClientID == "" || (cfg.ClientSecret == "") != cfg.PublicClient {
		return nil, fmt.Errorf("MockOIDCProvider: ClientID required, and ClientSecret exactly when PublicClient is unset")
	}

	listenAddr := cfg.ListenAddr
	if listenAddr == "" {
		listenAddr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen on %q: %w", listenAddr, err)
	}

	signingKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("generate signing key: %w", err)
	}

	idTokenOnlyClaims := cfg.IDTokenOnlyClaims
	if idTokenOnlyClaims == nil {
		idTokenOnlyClaims = []string{"groups", "roles"}
	}
	idTokenOnly := make(map[string]struct{}, len(idTokenOnlyClaims))
	for _, k := range idTokenOnlyClaims {
		idTokenOnly[k] = struct{}{}
	}

	p := &MockOIDCProvider{
		config:        cfg,
		issuer:        cfg.Issuer,
		listenAddr:    ln.Addr().String(),
		signingKey:    signingKey,
		kid:           randomToken("kid"),
		tenantID:      cmp.Or(cfg.TenantID, defaultTenantID),
		logger:        cfg.Logger,
		idTokenOnly:   idTokenOnly,
		usersBySub:    indexUsersBySub(cfg.Users),
		authCodes:     make(map[string]*oidcAuthCodeEntry),
		accessTokens:  make(map[string]*oidcAccessTokenEntry),
		refreshTokens: make(map[string]*oidcRefreshTokenEntry),
	}
	if p.issuer == "" {
		p.issuer = fmt.Sprintf("http://%s", ln.Addr().String())
	}

	janitorCtx, cancel := context.WithCancel(context.Background())
	p.janitorCancel = cancel
	go p.runJanitor(janitorCtx)

	p.server = &http.Server{Handler: p.newMux()}
	go p.server.Serve(ln) //nolint:errcheck

	return p, nil
}

func indexUsersBySub(users []FakeOIDCUser) map[string]*FakeOIDCUser {
	m := make(map[string]*FakeOIDCUser, len(users))
	for i := range users {
		if _, exists := m[users[i].Sub]; exists {
			continue
		}
		m[users[i].Sub] = &users[i]
	}
	return m
}

func (p *MockOIDCProvider) newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", p.handleDiscovery)
	mux.HandleFunc("/jwks.json", p.handleJWKS)
	mux.HandleFunc("/authorize", p.handleAuthorize)
	mux.HandleFunc("/token", p.handleToken)
	mux.HandleFunc("/userinfo", p.handleUserinfo)
	return mux
}

func (p *MockOIDCProvider) URL() string { return p.issuer }

func (p *MockOIDCProvider) Close() error {
	p.janitorCancel()
	p.mu.RLock()
	srv := p.server
	p.mu.RUnlock()
	return srv.Close()
}

func (p *MockOIDCProvider) runJanitor(ctx context.Context) {
	ticker := time.NewTicker(janitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			p.evictStale(now)
		}
	}
}

func (p *MockOIDCProvider) evictStale(now time.Time) {
	codeStale := func(e *oidcAuthCodeEntry) bool { return now.Sub(e.createdAt) > authCodeTTL }
	accessStale := func(e *oidcAccessTokenEntry) bool { return now.Sub(e.lastUsed) > tokenIdleTTL }
	refreshStale := func(e *oidcRefreshTokenEntry) bool { return now.Sub(e.lastUsed) > tokenIdleTTL }

	p.mu.RLock()
	codes := staleKeys(p.authCodes, codeStale)
	access := staleKeys(p.accessTokens, accessStale)
	refresh := staleKeys(p.refreshTokens, refreshStale)
	p.mu.RUnlock()

	if len(codes)+len(access)+len(refresh) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	deleteStale(p.authCodes, codes, codeStale)
	deleteStale(p.accessTokens, access, accessStale)
	deleteStale(p.refreshTokens, refresh, refreshStale)
}

func staleKeys[E any](m map[string]E, isStale func(E) bool) []string {
	var keys []string
	for k, e := range m {
		if isStale(e) {
			keys = append(keys, k)
		}
	}
	return keys
}

func deleteStale[E any](m map[string]E, keys []string, isStale func(E) bool) {
	for _, k := range keys {
		if e, ok := m[k]; ok && isStale(e) {
			delete(m, k)
		}
	}
}

func (p *MockOIDCProvider) Stop() error { return p.Close() }

func (p *MockOIDCProvider) Restart() error {
	ln, err := net.Listen("tcp", p.listenAddr)
	if err != nil {
		return fmt.Errorf("restart listen on %q: %w", p.listenAddr, err)
	}
	srv := &http.Server{Handler: p.newMux()}
	p.mu.Lock()
	p.server = srv
	p.mu.Unlock()
	go srv.Serve(ln) //nolint:errcheck
	return nil
}

func (p *MockOIDCProvider) RotateSigningKey() error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("rotate signing key: %w", err)
	}
	kid := randomToken("kid")
	p.mu.Lock()
	p.signingKey = key
	p.kid = kid
	p.mu.Unlock()
	return nil
}

func (p *MockOIDCProvider) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	doc := map[string]any{
		"issuer":                                p.issuer,
		"authorization_endpoint":                p.issuer + "/authorize",
		"token_endpoint":                        p.issuer + "/token",
		"userinfo_endpoint":                     p.issuer + "/userinfo",
		"jwks_uri":                              p.issuer + "/jwks.json",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"S256", "plain"},
		"scopes_supported":                      []string{"openid", "profile", "email"},
	}
	w.Header().Set(headerContentType, contentTypeJSON)
	_ = json.NewEncoder(w).Encode(doc)
}

func (p *MockOIDCProvider) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	p.mu.RLock()
	key, kid := p.signingKey, p.kid
	p.mu.RUnlock()
	w.Header().Set(headerContentType, contentTypeJSON)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"keys": []map[string]any{
			rsaJWK(&key.PublicKey, kid),
		},
	})
}

func rsaJWK(pub *rsa.PublicKey, kid string) map[string]any {
	eBytes := big.NewInt(int64(pub.E)).Bytes()
	return map[string]any{
		"kty": "RSA",
		"alg": "RS256",
		"use": "sig",
		"kid": kid,
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(eBytes),
	}
}

func (p *MockOIDCProvider) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	p.authorizeCount.Add(1)

	q := r.URL.Query()
	if q.Get("client_id") != p.config.ClientID {
		http.Error(w, "invalid_client", http.StatusBadRequest)
		return
	}
	if q.Get("response_type") != "code" {
		http.Error(w, "unsupported_response_type", http.StatusBadRequest)
		return
	}
	redirectURI := q.Get("redirect_uri")
	if redirectURI == "" {
		http.Error(w, "invalid_request: redirect_uri required", http.StatusBadRequest)
		return
	}
	sub := q.Get("fake_user_sub")
	if sub == "" && p.config.EnablePicker {
		p.renderPicker(w, r.URL.RawQuery)
		return
	}
	if !p.knowsUser(sub) {
		http.Error(w, "access_denied: unknown fake_user_sub", http.StatusBadRequest)
		return
	}

	code := randomToken("code")
	p.mu.Lock()
	p.authCodes[code] = &oidcAuthCodeEntry{
		UserSub:       sub,
		RedirectURI:   redirectURI,
		CodeChallenge: q.Get("code_challenge"),
		CodeMethod:    q.Get("code_challenge_method"),
		Nonce:         q.Get("nonce"),
		createdAt:     time.Now(),
	}
	p.mu.Unlock()
	p.logEvent("user authorized", "sub", sub)

	var redirect string
	if u, perr := url.Parse(redirectURI); perr == nil {
		qq := u.Query()
		qq.Set("code", code)
		if state := q.Get("state"); state != "" {
			qq.Set("state", state)
		}
		u.RawQuery = qq.Encode()
		redirect = u.String()
	} else {
		redirect = redirectURI + "?code=" + url.QueryEscape(code)
		if state := q.Get("state"); state != "" {
			redirect += "&state=" + url.QueryEscape(state)
		}
	}
	http.Redirect(w, r, redirect, http.StatusFound)
}

func (p *MockOIDCProvider) knowsUser(sub string) bool {
	if sub == "" {
		return false
	}
	_, ok := p.usersBySub[sub]
	return ok
}

func (p *MockOIDCProvider) logEvent(msg string, args ...any) {
	if p.logger != nil {
		p.logger.Debug(msg, args...)
	}
}

func (p *MockOIDCProvider) renderPicker(w http.ResponseWriter, rawQuery string) {
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"><title>dev-idp login</title>`)
	b.WriteString(`<style>body{font:15px system-ui;margin:3rem auto;max-width:560px}` +
		`a.u{display:block;padding:.8rem 1rem;margin:.5rem 0;border:1px solid #ccc;border-radius:8px;text-decoration:none;color:#111}` +
		`a.u:hover{background:#f3f4f6}small{color:#666}` +
		`input,button{font:15px system-ui;padding:.6rem .8rem}</style></head><body>`)
	b.WriteString(`<h2>Pick a user to sign in as</h2>`)
	if len(p.config.Users) <= pickerUserLimit {
		for _, u := range p.config.Users {
			href := "/authorize?" + rawQuery + "&fake_user_sub=" + url.QueryEscape(u.Sub)
			label := u.Email
			if label == "" {
				label = u.Sub
			}
			fmt.Fprintf(&b, `<a class="u" href="%s"><b>%s</b><br><small>%s</small></a>`,
				html.EscapeString(href), html.EscapeString(label), html.EscapeString(groupsLabel(u)))
		}
	} else {
		fmt.Fprintf(&b, `<form method="GET" action="/authorize">%s`,
			hiddenQueryFields(rawQuery))
		b.WriteString(`<input name="fake_user_sub" placeholder="user sub" autofocus> ` +
			`<button type="submit">Sign in</button></form>`)
	}
	b.WriteString(`</body></html>`)
	w.Header().Set(headerContentType, "text/html; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

func hiddenQueryFields(rawQuery string) string {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for key, vals := range values {
		if key == "fake_user_sub" {
			continue
		}
		for _, v := range vals {
			fmt.Fprintf(&b, `<input type="hidden" name="%s" value="%s">`,
				html.EscapeString(key), html.EscapeString(v))
		}
	}
	return b.String()
}

func groupsLabel(u FakeOIDCUser) string {
	groups, ok := u.Claims["groups"].([]string)
	if !ok || len(groups) == 0 {
		return "no groups"
	}
	return "groups: " + strings.Join(groups, ", ")
}

func (p *MockOIDCProvider) handleToken(w http.ResponseWriter, r *http.Request) {
	p.tokenCount.Add(1)

	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed token request")
		return
	}
	clientID, clientSecret, hasBasic := r.BasicAuth()
	if !hasBasic {
		clientID = r.PostForm.Get("client_id")
		clientSecret = r.PostForm.Get("client_secret")
	}
	if clientID != p.config.ClientID || clientSecret != p.config.ClientSecret {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "")
		return
	}

	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		p.handleAuthCodeGrant(w, r)
	case "refresh_token":
		p.handleRefreshGrant(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "")
	}
}

func (p *MockOIDCProvider) handleAuthCodeGrant(w http.ResponseWriter, r *http.Request) {
	code := r.PostForm.Get("code")
	redirectURI := r.PostForm.Get("redirect_uri")

	p.mu.Lock()
	entry, ok := p.authCodes[code]
	if !ok || entry.Used || entry.RedirectURI != redirectURI {
		p.mu.Unlock()
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "code is invalid, expired, or already used")
		return
	}
	challenge, method, nonce, userSub := entry.CodeChallenge, entry.CodeMethod, entry.Nonce, entry.UserSub
	p.mu.Unlock()

	if p.config.PublicClient && challenge == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "a public client must use PKCE")
		return
	}

	if !verifyPKCE(challenge, method, r.PostForm.Get("code_verifier")) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}

	p.mu.Lock()
	entry, ok = p.authCodes[code]
	if !ok || entry.Used {
		p.mu.Unlock()
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "code already used")
		return
	}
	entry.Used = true
	p.mu.Unlock()

	p.issueTokenResponse(w, "authorization_code", userSub, nonce, "")
}

func (p *MockOIDCProvider) handleRefreshGrant(w http.ResponseWriter, r *http.Request) {
	refreshToken := r.PostForm.Get("refresh_token")
	p.mu.Lock()
	entry, ok := p.refreshTokens[refreshToken]
	var userSub string
	if ok {
		userSub = entry.UserSub
		entry.lastUsed = time.Now()
		if p.config.RotateRefreshTokens {
			delete(p.refreshTokens, refreshToken)
			refreshToken = ""
		}
	}
	p.mu.Unlock()
	if !ok {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "unknown refresh_token")
		return
	}
	p.issueTokenResponse(w, "refresh_token", userSub, "", refreshToken)
}

func (p *MockOIDCProvider) issueTokenResponse(w http.ResponseWriter, grant, userSub, nonce, refreshToken string) {
	user := p.lookupUser(userSub)
	if user == nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "unknown user for grant")
		return
	}
	idToken, err := p.issueIDToken(user, nonce)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "id_token signing failure")
		return
	}

	var claims map[string]any
	if p.config.AccessTokenClaims != nil {
		claims = p.config.AccessTokenClaims(userSub, grant)
	}
	now := time.Now()
	accessToken := randomToken("at")
	p.mu.Lock()
	p.accessTokens[accessToken] = &oidcAccessTokenEntry{UserSub: userSub, Claims: claims, lastUsed: now}
	if refreshToken == "" {
		refreshToken = randomToken("rt")
		p.refreshTokens[refreshToken] = &oidcRefreshTokenEntry{UserSub: userSub, lastUsed: now}
	}
	p.mu.Unlock()
	p.logEvent("token issued", "grant", grant, "sub", userSub)

	w.Header().Set(headerContentType, contentTypeJSON)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  accessToken,
		"id_token":      idToken,
		"refresh_token": refreshToken,
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
}

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	body := map[string]string{"error": code}
	if desc != "" {
		body["error_description"] = desc
	}
	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func verifyPKCE(challenge, method, verifier string) bool {
	if challenge == "" {
		return true
	}
	if verifier == "" {
		return false
	}
	switch method {
	case "", "plain":
		return verifier == challenge
	case "S256":
		sum := sha256.Sum256([]byte(verifier))
		return base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
	default:
		return false
	}
}

func (p *MockOIDCProvider) lookupUser(sub string) *FakeOIDCUser {
	return p.usersBySub[sub]
}

func (p *MockOIDCProvider) issueIDToken(u *FakeOIDCUser, nonce string) (string, error) {
	p.mu.RLock()
	signingKey, kid := p.signingKey, p.kid
	p.mu.RUnlock()
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":                p.issuer,
		"aud":                p.config.ClientID,
		"sub":                u.Sub,
		"email":              u.Email,
		"name":               u.Name,
		"iat":                now.Unix(),
		"exp":                now.Add(time.Hour).Unix(),
		"nbf":                now.Unix(),
		"ver":                "2.0",
		"tid":                p.tenantID,
		"oid":                u.Sub,
		"preferred_username": cmp.Or(u.Email, u.Sub),
	}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	for k, v := range u.Claims {
		claims[k] = v
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	return tok.SignedString(signingKey)
}

func (p *MockOIDCProvider) handleUserinfo(w http.ResponseWriter, r *http.Request) {
	p.userinfoCount.Add(1)

	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		http.Error(w, "missing bearer", http.StatusUnauthorized)
		return
	}
	token := auth[len(prefix):]
	p.mu.Lock()
	entry, ok := p.accessTokens[token]
	var userSub string
	if ok {
		userSub = entry.UserSub
		entry.lastUsed = time.Now()
	}
	p.mu.Unlock()
	if !ok {
		http.Error(w, "invalid_token", http.StatusUnauthorized)
		return
	}
	user := p.lookupUser(userSub)
	if user == nil {
		http.Error(w, "user vanished", http.StatusInternalServerError)
		return
	}
	info := map[string]any{
		"sub":   user.Sub,
		"email": user.Email,
		"name":  user.Name,
	}
	for k, v := range user.Claims {
		if _, idTokenOnly := p.idTokenOnly[k]; idTokenOnly {
			continue
		}
		info[k] = v
	}
	w.Header().Set(headerContentType, contentTypeJSON)
	_ = json.NewEncoder(w).Encode(info)
}

func (p *MockOIDCProvider) IssueAccessToken(sub string) (string, error) {
	if p.lookupUser(sub) == nil {
		return "", fmt.Errorf("MockOIDCProvider: unknown sub %q", sub)
	}
	token := randomToken("at")
	p.mu.Lock()
	p.accessTokens[token] = &oidcAccessTokenEntry{UserSub: sub, lastUsed: time.Now()}
	p.mu.Unlock()
	return token, nil
}

func (p *MockOIDCProvider) AccessToken(token string) (sub string, claims map[string]any, ok bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	entry, ok := p.accessTokens[token]
	if !ok {
		return "", nil, false
	}
	return entry.UserSub, entry.Claims, true
}

func (p *MockOIDCProvider) RevokeTokens(sub string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for token, entry := range p.accessTokens {
		if entry.UserSub == sub {
			delete(p.accessTokens, token)
		}
	}
	for token, entry := range p.refreshTokens {
		if entry.UserSub == sub {
			delete(p.refreshTokens, token)
		}
	}
}

func (p *MockOIDCProvider) AuthorizeCount() int { return int(p.authorizeCount.Load()) }

func (p *MockOIDCProvider) TokenCount() int { return int(p.tokenCount.Load()) }

func (p *MockOIDCProvider) UserinfoCount() int { return int(p.userinfoCount.Load()) }

func randomToken(prefix string) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(b)
}
