// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"google.golang.org/api/googleapi"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

var threadNameRE = regexp.MustCompile(`^spaces/[A-Za-z0-9_-]+/threads/[A-Za-z0-9_-]+$`)

const (
	threadContextSource  = "GOOGLE CHAT"
	threadContextSubject = "Google Chat thread this message was posted in"
	unknownSpeaker       = "Unknown"
)

func (a *Adapter) threadContext(ev *inboundEvent) string {
	if a.threads == nil || ev.IsDM || !ev.ThreadReply || !threadNameRE.MatchString(ev.ThreadName) {
		return ""
	}
	limit := a.cfg.ThreadHistoryLimit
	ctx, cancel := context.WithTimeout(a.ctx, threadHistoryFetchTimeout)
	defer cancel()
	msgs, err := a.threads.RecentThreadMessages(ctx, ev.SpaceName, ev.ThreadName, limit+1)
	if err != nil {
		a.warnThreadHistory(err)
		return ""
	}
	lines := make([]string, 0, len(msgs))
	for _, m := range msgs {
		text := strings.TrimSpace(m.Text)
		if m.Name == ev.MessageName || text == "" {
			continue
		}
		lines = append(lines, cmp.Or(strings.TrimSpace(m.SenderDisplay), unknownSpeaker)+": "+text)
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	if len(lines) == 0 {
		return ""
	}
	return entrypoint.FenceUntrustedConversation(threadContextSource, threadContextSubject, strings.Join(lines, "\n"))
}

const (
	historyFailureForbidden = "forbidden"
	historyFailureRejected  = "rejected"
	historyFailureTransient = "transient"
)

func (a *Adapter) warnThreadHistory(err error) {
	cause, status := classifyThreadHistoryFailure(err)
	if !a.historyWarnings.allow("thread_history_"+cause, threadHistoryWarnWindow) {
		return
	}
	logged := loggableHistoryError(err)
	switch cause {
	case historyFailureForbidden:
		a.logger.Warn("google chat thread history unavailable: Google refused the read; a Workspace administrator must approve the app's chat.app.messages.readonly scope", "error", logged)
	case historyFailureRejected:
		a.logger.Warn("google chat thread history read rejected; tasks run without it", "httpStatus", status, "error", logged)
	default:
		a.logger.Warn("google chat thread history read failed; tasks run without it", "error", logged)
	}
}

func classifyThreadHistoryFailure(err error) (cause string, status int) {
	_, permanent := errors.AsType[*permanentError](err)
	if !permanent && !errors.Is(err, errMessageGone) {
		return historyFailureTransient, 0
	}
	if apiErr, ok := errors.AsType[*googleapi.Error](err); ok {
		status = apiErr.Code
	}
	if status == http.StatusForbidden {
		return historyFailureForbidden, status
	}
	return historyFailureRejected, status
}

func loggableHistoryError(err error) error {
	if apiErr, ok := errors.AsType[*googleapi.Error](err); ok {
		return apiErr
	}
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		return fmt.Errorf("%s: %w", urlErr.Op, urlErr.Err)
	}
	return err
}

func (a *Adapter) conversationContext(ev *inboundEvent, swapped bool) string {
	parts := make([]string, 0, 2)
	if swapped {
		parts = append(parts, entrypoint.ChannelRunAsSystemNote)
	}
	if thread := a.threadContext(ev); thread != "" {
		parts = append(parts, thread)
	}
	return strings.Join(parts, "\n\n")
}
