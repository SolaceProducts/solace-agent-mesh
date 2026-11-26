// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

const noEmailMarker = "_NO_EMAIL_"

type EmailCache struct {
	mu        sync.RWMutex
	entries   map[string]emailEntry
	ttl       time.Duration
	tenantID  string
	botClient *BotClient
	logger    *slog.Logger
}

type emailEntry struct {
	email   string
	expires time.Time
}

func NewEmailCache(tenantID string, ttl time.Duration, botClient *BotClient, logger *slog.Logger) *EmailCache {
	return &EmailCache{
		entries:   make(map[string]emailEntry),
		ttl:       ttl,
		tenantID:  tenantID,
		botClient: botClient,
		logger:    logger,
	}
}

func (c *EmailCache) ResolveIdentity(ctx context.Context, userID, userName, aadObjectID, serviceURL, conversationID string) *auth.Subject {
	if c.tenantID != "" && c.botClient != nil && serviceURL != "" && conversationID != "" {
		if email := c.lookupEmail(ctx, userID, serviceURL, conversationID); email != "" {
			return auth.ForTeamsUser(email, email, userName, userID, aadObjectID, "azure_ad_email")
		}
	}

	if aadObjectID != "" {
		return auth.ForTeamsUser(aadObjectID, "", userName, userID, aadObjectID, "azure_ad")
	}

	return auth.ForTeamsUser(fmt.Sprintf("teams:%s", userID), "", userName, userID, aadObjectID, "teams_fallback")
}

func (c *EmailCache) lookupEmail(ctx context.Context, memberID, serviceURL, conversationID string) string {
	if c.ttl > 0 {
		c.mu.RLock()
		entry, ok := c.entries[memberID]
		c.mu.RUnlock()
		if ok && time.Now().Before(entry.expires) {
			if entry.email == noEmailMarker {
				return ""
			}
			return entry.email
		}
	}

	email, err := c.fetchMemberEmail(ctx, memberID, serviceURL, conversationID)
	if err != nil {
		c.logger.Debug("member lookup failed; not caching negative result", "memberID", memberID, "error", err)
		return ""
	}
	if email != "" {
		c.cacheResult(memberID, email)
		return email
	}

	c.cacheResult(memberID, noEmailMarker)
	return ""
}

type memberResponse struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Email             string `json:"email"`
	UserPrincipalName string `json:"userPrincipalName"`
	AadObjectID       string `json:"aadObjectId"`
}

func (c *EmailCache) fetchMemberEmail(ctx context.Context, memberID, serviceURL, conversationID string) (string, error) {
	if c.botClient == nil {
		return "", fmt.Errorf("no bot client configured")
	}

	token, err := c.botClient.getToken(ctx)
	if err != nil {
		return "", fmt.Errorf("get token for member lookup: %w", err)
	}

	url := c.botClient.buildURL(serviceURL, fmt.Sprintf("/v3/conversations/%s/members/%s", conversationID, memberID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create member lookup request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.botClient.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("member lookup request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		if isTransientStatus(resp.StatusCode) {
			return "", fmt.Errorf("member lookup returned status %d", resp.StatusCode)
		}
		c.logger.Debug("member lookup returned definitive client error; caching no-email", "httpStatus", resp.StatusCode, "memberID", memberID)
		return "", nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read member lookup body: %w", err)
	}

	var member memberResponse
	if err := json.Unmarshal(body, &member); err != nil {
		return "", fmt.Errorf("parse member response: %w", err)
	}

	if member.Email != "" {
		c.logger.Debug("resolved email via member lookup", "memberID", memberID)
		return member.Email, nil
	}
	if member.UserPrincipalName != "" {
		c.logger.Debug("resolved UPN via member lookup", "memberID", memberID)
		return member.UserPrincipalName, nil
	}

	return "", nil
}

func isTransientStatus(code int) bool {
	return code >= 500 || code == http.StatusTooManyRequests || code == http.StatusRequestTimeout
}

func (c *EmailCache) cacheResult(key, email string) {
	if c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	c.entries[key] = emailEntry{
		email:   email,
		expires: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}
