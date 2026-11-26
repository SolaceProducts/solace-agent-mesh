// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type MockOAuthProviderConfig struct {
	ClientID               string
	ClientSecret           string
	TokenExpirySeconds     int64
	RotateRefreshTokens    bool
	NoRefreshToken         bool
	RejectAuthCodeExchange bool
}

type MockOAuthProvider struct {
	listener net.Listener
	server   *http.Server

	mu     sync.Mutex
	config MockOAuthProviderConfig

	authCodes     map[string]*authCodeEntry
	refreshTokens map[string]*refreshTokenEntry
	accessTokens  map[string]*accessTokenEntry
	tokenRequests []TokenRequestLog
}

type authCodeEntry struct {
	UserID        string
	CodeChallenge string
	RedirectURI   string
	ClientID      string
	Used          bool
	ExpiresAt     time.Time
}

type refreshTokenEntry struct {
	UserID  string
	Revoked bool
}

type accessTokenEntry struct {
	UserID    string
	ExpiresAt time.Time
}

type TokenRequestLog struct {
	GrantType string
	UserID    string
	Timestamp time.Time
	Success   bool
	ErrorCode string
}

func NewMockOAuthProvider(cfg MockOAuthProviderConfig) (*MockOAuthProvider, error) {
	if cfg.TokenExpirySeconds == 0 {
		cfg.TokenExpirySeconds = 3600
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	p := &MockOAuthProvider{
		listener:      l,
		config:        cfg,
		authCodes:     make(map[string]*authCodeEntry),
		refreshTokens: make(map[string]*refreshTokenEntry),
		accessTokens:  make(map[string]*accessTokenEntry),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", p.handleToken)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	p.server = &http.Server{Handler: mux}
	go p.server.Serve(l) //nolint:errcheck

	return p, nil
}

func (p *MockOAuthProvider) URL() string {
	return fmt.Sprintf("http://%s", p.listener.Addr().String())
}

func (p *MockOAuthProvider) TokenURL() string {
	return p.URL() + "/token"
}

func (p *MockOAuthProvider) AuthorizationURL() string {
	return p.URL() + "/authorize"
}

func (p *MockOAuthProvider) Close() {
	_ = p.server.Close()
}

func (p *MockOAuthProvider) IssueAuthorizationCode(userID, codeChallenge, redirectURI, clientID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	code := "authcode-" + randomNonce()
	p.authCodes[code] = &authCodeEntry{
		UserID:        userID,
		CodeChallenge: codeChallenge,
		RedirectURI:   redirectURI,
		ClientID:      clientID,
		Used:          false,
		ExpiresAt:     time.Now().Add(5 * time.Minute),
	}
	return code
}

func (p *MockOAuthProvider) ValidateAccessToken(token string) (userID string, valid bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.accessTokens[token]
	if !ok {
		return "", false
	}
	if time.Now().After(entry.ExpiresAt) {
		return entry.UserID, false
	}
	return entry.UserID, true
}

func (p *MockOAuthProvider) IsAccessTokenValid(token string) bool {
	_, valid := p.ValidateAccessToken(token)
	return valid
}

func (p *MockOAuthProvider) RevokeAllUserTokens(userID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, entry := range p.refreshTokens {
		if entry.UserID == userID {
			entry.Revoked = true
		}
	}
}

func (p *MockOAuthProvider) SetTokenExpiry(seconds int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.config.TokenExpirySeconds = seconds
}

func (p *MockOAuthProvider) AuthCodeGrantCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	count := 0
	for _, r := range p.tokenRequests {
		if r.GrantType == "authorization_code" && r.Success {
			count++
		}
	}
	return count
}

func (p *MockOAuthProvider) RefreshGrantCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	count := 0
	for _, r := range p.tokenRequests {
		if r.GrantType == "refresh_token" && r.Success {
			count++
		}
	}
	return count
}

func (p *MockOAuthProvider) AccessTokenForUser(userID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	var latest string
	var latestTime time.Time
	for token, entry := range p.accessTokens {
		if entry.UserID == userID {
			if latest == "" || entry.ExpiresAt.After(latestTime) {
				latest = token
				latestTime = entry.ExpiresAt
			}
		}
	}
	return latest
}

func (p *MockOAuthProvider) IsRefreshTokenRevoked(token string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.refreshTokens[token]
	if !ok {
		return false
	}
	return entry.Revoked
}

func (p *MockOAuthProvider) TokenRequestLogs() []TokenRequestLog {
	p.mu.Lock()
	defer p.mu.Unlock()

	cp := make([]TokenRequestLog, len(p.tokenRequests))
	copy(cp, p.tokenRequests)
	return cp
}

func (p *MockOAuthProvider) ErrorCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	count := 0
	for _, r := range p.tokenRequests {
		if !r.Success {
			count++
		}
	}
	return count
}

func (p *MockOAuthProvider) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		p.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
		return
	}

	grantType := r.Form.Get("grant_type")
	switch grantType {
	case "authorization_code":
		p.handleAuthCodeGrant(w, r)
	case "refresh_token":
		p.handleRefreshGrant(w, r)
	default:
		p.logRequest(grantType, "", false, "unsupported_grant_type")
		p.writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type",
			fmt.Sprintf("grant_type %q not supported", grantType))
	}
}

func (p *MockOAuthProvider) handleAuthCodeGrant(w http.ResponseWriter, r *http.Request) {
	code := r.Form.Get("code")
	codeVerifier := r.Form.Get("code_verifier")
	clientID, clientSecret := p.extractClientCredentials(r)

	p.mu.Lock()
	reject := p.config.RejectAuthCodeExchange
	p.mu.Unlock()
	if reject {
		p.logRequest("authorization_code", "", false, "invalid_grant")
		p.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code rejected by test harness")
		return
	}

	p.mu.Lock()
	entry, ok := p.authCodes[code]

	if !ok {
		p.mu.Unlock()
		p.logRequest("authorization_code", "", false, "invalid_grant")
		p.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code not found")
		return
	}

	if entry.Used {
		p.mu.Unlock()
		p.logRequest("authorization_code", entry.UserID, false, "invalid_grant")
		p.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code already used")
		return
	}

	if time.Now().After(entry.ExpiresAt) {
		p.mu.Unlock()
		p.logRequest("authorization_code", entry.UserID, false, "invalid_grant")
		p.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code expired")
		return
	}

	entry.Used = true
	userID := entry.UserID
	storedChallenge := entry.CodeChallenge
	expirySeconds := p.config.TokenExpirySeconds
	noRefresh := p.config.NoRefreshToken
	p.mu.Unlock()

	if p.config.ClientID != "" {
		if clientID != p.config.ClientID {
			p.logRequest("authorization_code", userID, false, "invalid_client")
			p.writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "client_id mismatch")
			return
		}
		if p.config.ClientSecret != "" && clientSecret != p.config.ClientSecret {
			p.logRequest("authorization_code", userID, false, "invalid_client")
			p.writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "client_secret mismatch")
			return
		}
	}

	if storedChallenge != "" {
		h := sha256.Sum256([]byte(codeVerifier))
		computedChallenge := base64.RawURLEncoding.EncodeToString(h[:])
		if computedChallenge != storedChallenge {
			p.logRequest("authorization_code", userID, false, "invalid_grant")
			p.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
			return
		}
	}

	accessToken := fmt.Sprintf("access-%s-%s", userID, randomNonce())
	expiresAt := time.Now().Add(time.Duration(expirySeconds) * time.Second)

	p.mu.Lock()
	p.accessTokens[accessToken] = &accessTokenEntry{
		UserID:    userID,
		ExpiresAt: expiresAt,
	}

	resp := map[string]any{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   expirySeconds,
	}

	if !noRefresh {
		refreshToken := fmt.Sprintf("refresh-%s-%s", userID, randomNonce())
		p.refreshTokens[refreshToken] = &refreshTokenEntry{
			UserID:  userID,
			Revoked: false,
		}
		resp["refresh_token"] = refreshToken
	}
	p.mu.Unlock()

	p.logRequest("authorization_code", userID, true, "")
	p.writeJSON(w, http.StatusOK, resp)
}

func (p *MockOAuthProvider) handleRefreshGrant(w http.ResponseWriter, r *http.Request) {
	refreshToken := r.Form.Get("refresh_token")
	clientID, clientSecret := p.extractClientCredentials(r)

	if p.config.ClientID != "" {
		if clientID != p.config.ClientID {
			p.logRequest("refresh_token", "", false, "invalid_client")
			p.writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "client_id mismatch")
			return
		}
		if p.config.ClientSecret != "" && clientSecret != p.config.ClientSecret {
			p.logRequest("refresh_token", "", false, "invalid_client")
			p.writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "client_secret mismatch")
			return
		}
	}

	p.mu.Lock()
	entry, ok := p.refreshTokens[refreshToken]
	if !ok {
		p.mu.Unlock()
		p.logRequest("refresh_token", "", false, "invalid_grant")
		p.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token not found")
		return
	}
	if entry.Revoked {
		p.mu.Unlock()
		p.logRequest("refresh_token", entry.UserID, false, "invalid_grant")
		p.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token revoked")
		return
	}

	userID := entry.UserID
	expirySeconds := p.config.TokenExpirySeconds
	rotate := p.config.RotateRefreshTokens

	if rotate {
		entry.Revoked = true
	}

	accessToken := fmt.Sprintf("access-%s-%s", userID, randomNonce())
	p.accessTokens[accessToken] = &accessTokenEntry{
		UserID:    userID,
		ExpiresAt: time.Now().Add(time.Duration(expirySeconds) * time.Second),
	}

	resp := map[string]any{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   expirySeconds,
	}

	if rotate {
		newRefresh := fmt.Sprintf("refresh-%s-%s", userID, randomNonce())
		p.refreshTokens[newRefresh] = &refreshTokenEntry{
			UserID:  userID,
			Revoked: false,
		}
		resp["refresh_token"] = newRefresh
	}
	p.mu.Unlock()

	p.logRequest("refresh_token", userID, true, "")
	p.writeJSON(w, http.StatusOK, resp)
}

func (p *MockOAuthProvider) extractClientCredentials(r *http.Request) (clientID, clientSecret string) {
	clientID = r.Form.Get("client_id")
	clientSecret = r.Form.Get("client_secret")
	if clientID != "" {
		return clientID, clientSecret
	}

	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Basic ") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(auth, "Basic "))
		if err == nil {
			parts := strings.SplitN(string(decoded), ":", 2)
			if len(parts) == 2 {
				return parts[0], parts[1]
			}
		}
	}

	return "", ""
}

func (p *MockOAuthProvider) logRequest(grantType, userID string, success bool, errorCode string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tokenRequests = append(p.tokenRequests, TokenRequestLog{
		GrantType: grantType,
		UserID:    userID,
		Timestamp: time.Now(),
		Success:   success,
		ErrorCode: errorCode,
	})
}

func (p *MockOAuthProvider) writeOAuthError(w http.ResponseWriter, status int, errorCode, description string) {
	p.writeJSON(w, status, map[string]string{
		"error":             errorCode,
		"error_description": description,
	})
}

func (p *MockOAuthProvider) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func randomNonce() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("%x", buf)
}
