// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
	goslack "github.com/slack-go/slack"
)

const noEmailMarker = "_NO_EMAIL_"

var mentionPattern = regexp.MustCompile(`<@([UW][A-Z0-9]+)(?:\|[^>]*)?>`)

type EmailCache struct {
	mu      sync.RWMutex
	entries map[string]emailEntry
	ttl     time.Duration
	api     slackapi.SlackAPIAdapter
	logger  *slog.Logger
}

type emailEntry struct {
	email    string
	realName string
	expires  time.Time
}

func NewEmailCache(api slackapi.SlackAPIAdapter, ttl time.Duration, logger *slog.Logger) *EmailCache {
	return &EmailCache{
		entries: make(map[string]emailEntry),
		ttl:     ttl,
		api:     api,
		logger:  logger,
	}
}

func (c *EmailCache) ResolveIdentity(slackUserID, slackTeamID string) *auth.Subject {
	email := c.lookupEmail(slackUserID)

	if email != "" {
		return auth.ForSlackUser(email, email, slackUserID, slackTeamID)
	}
	return auth.ForSlackUser(fmt.Sprintf("slack:%s:%s", slackTeamID, slackUserID), "", slackUserID, slackTeamID)
}

func (c *EmailCache) lookupEmail(slackUserID string) string {
	email, _ := c.lookupProfile(context.Background(), slackUserID)
	return email
}

func (c *EmailCache) ResolveMentions(ctx context.Context, text string) string {
	if !strings.Contains(text, "<@") {
		return text
	}
	resolved := make(map[string]string)
	return mentionPattern.ReplaceAllStringFunc(text, func(token string) string {
		userID := mentionPattern.FindStringSubmatch(token)[1]
		label, ok := resolved[userID]
		if !ok {
			email, realName := c.lookupProfile(ctx, userID)
			label = strings.Join(strings.Fields(cmp.Or(email, realName)), " ")
			resolved[userID] = label
		}
		if label == "" {
			return token
		}
		return label
	})
}

func (c *EmailCache) lookupProfile(ctx context.Context, slackUserID string) (email, realName string) {
	if c.ttl > 0 {
		c.mu.RLock()
		entry, ok := c.entries[slackUserID]
		c.mu.RUnlock()
		if ok && time.Now().Before(entry.expires) {
			if entry.email == noEmailMarker {
				return "", entry.realName
			}
			return entry.email, entry.realName
		}
	}

	profile, err := c.api.GetUserProfile(ctx, &goslack.GetUserProfileParameters{
		UserID: slackUserID,
	})
	if err != nil {
		c.logger.Warn("failed to fetch Slack user profile", "userID", slackUserID, "error", err)
		return "", ""
	}

	email = profile.Email
	realName = profile.RealNameNormalized
	if email == "" {
		c.logger.Debug("no email in Slack profile", "userID", slackUserID)
		c.cacheProfile(slackUserID, noEmailMarker, realName)
		return "", realName
	}

	c.cacheProfile(slackUserID, email, realName)
	return email, realName
}

func (c *EmailCache) cacheProfile(slackUserID, email, realName string) {
	if c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	c.entries[slackUserID] = emailEntry{
		email:    email,
		realName: realName,
		expires:  time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}
