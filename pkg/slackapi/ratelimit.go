// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slackapi

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type tier int

const (
	tier1 tier = iota
	tier2
	tier3
	tier4
	special
)

const (
	methodChatPostMessage         = "chat.postMessage"
	methodChatPostEphemeral       = "chat.postEphemeral"
	methodChatUpdate              = "chat.update"
	methodChatDelete              = "chat.delete"
	methodChatStartStream         = "chat.startStream"
	methodChatAppendStream        = "chat.appendStream"
	methodChatStopStream          = "chat.stopStream"
	methodReactionsAdd            = "reactions.add"
	methodReactionsRemove         = "reactions.remove"
	methodFilesCompleteUpload     = "files.completeUploadExternal"
	methodFilesGetUploadURL       = "files.getUploadURLExternal"
	methodFilesInfo               = "files.info"
	methodUsersInfo               = "users.info"
	methodUsersProfileGet         = "users.profile.get"
	methodUsersLookupByEmail      = "users.lookupByEmail"
	methodConversationsInfo       = "conversations.info"
	methodConversationsList       = "conversations.list"
	methodConversationsOpen       = "conversations.open"
	methodConversationsReplies    = "conversations.replies"
	methodConversationsHistory    = "conversations.history"
	methodViewsOpen               = "views.open"
	methodAgentsSessionsSetStatus = "agents.sessions.setStatus"
)

var tierIntervals = map[tier]time.Duration{
	tier1:   2000 * time.Millisecond,
	tier2:   1500 * time.Millisecond,
	tier3:   1000 * time.Millisecond,
	tier4:   500 * time.Millisecond,
	special: 1000 * time.Millisecond,
}

var methodTiers = map[string]tier{
	methodChatPostMessage:   special,
	methodChatPostEphemeral: special,

	methodChatStartStream:  special,
	methodChatAppendStream: tier4,
	methodChatStopStream:   tier2,

	methodViewsOpen: tier4,

	methodChatUpdate:          tier2,
	methodChatDelete:          tier2,
	methodReactionsAdd:        tier2,
	methodReactionsRemove:     tier2,
	methodFilesCompleteUpload: tier2,
	methodConversationsList:   tier2,

	methodFilesInfo:            tier3,
	methodFilesGetUploadURL:    tier3,
	methodUsersInfo:            tier3,
	methodUsersProfileGet:      tier3,
	methodUsersLookupByEmail:   tier3,
	methodConversationsInfo:    tier3,
	methodConversationsOpen:    tier3,
	methodConversationsReplies: tier3,
	methodConversationsHistory: tier3,

	methodAgentsSessionsSetStatus: tier3,
}

const defaultTier = tier3

func tierForMethod(method string) tier {
	if t, ok := methodTiers[method]; ok {
		return t
	}
	return defaultTier
}

type rateLimiter struct {
	mu            sync.Mutex
	lastCallTimes map[tier]time.Time
	disabled      bool
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{lastCallTimes: make(map[tier]time.Time)}
}

func newRateLimiterDisabled() *rateLimiter {
	return &rateLimiter{lastCallTimes: make(map[tier]time.Time), disabled: true}
}

func (r *rateLimiter) throttle(ctx context.Context, method string) error {
	if r.disabled {
		return nil
	}
	t := tierForMethod(method)
	interval := tierIntervals[t]

	r.mu.Lock()
	last := r.lastCallTimes[t]
	now := time.Now()
	var wait time.Duration
	if elapsed := now.Sub(last); elapsed < interval {
		wait = interval - elapsed
	}
	r.lastCallTimes[t] = now.Add(wait)
	r.mu.Unlock()

	if wait > 0 {
		slog.Default().With("logger_name", "slack-ratelimit").Debug("rate limiter throttling", "method", method, "tier", t, "wait", wait)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
