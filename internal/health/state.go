// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package health

import (
	"cmp"
	"fmt"
	"slices"
	"sync"
	"time"
)

type Severity int

const (
	Degraded Severity = iota
	Fatal
)

func (s Severity) String() string {
	if s == Fatal {
		return "fatal"
	}
	return "degraded"
}

type detail string

const (
	DetailQueueGone                 detail = "The queue no longer exists. Restart to restore message delivery."
	DetailProvisionCooldown         detail = "The event broker queue is temporarily unavailable due to a capacity or connectivity issue. Retrying automatically."
	DetailExistingQueueBindRetrying detail = "The pre-provisioned event broker queue is unavailable. Retrying automatically."
	DetailConsumeQueueUnavailable   detail = "The pre-provisioned event broker queue is temporarily missing or inaccessible. Retrying automatically."
	DetailTemporaryReceive          detail = "A temporary event broker error occurred. Retrying automatically."
	DetailIngressDown               detail = "The entrypoint is unable to receive inbound webhooks. Retrying automatically."
	DetailStartFailed               detail = "The entrypoint failed to start. Retrying automatically."
	DetailSignerCertsUnavailable    detail = "The entrypoint is unable to refresh the certificates that verify inbound requests. Retrying automatically."
	DetailRemoteFetchFailed         detail = "Unable to reach the peer agent. Retrying automatically."
	DetailRemoteCardUntrusted       detail = "The peer agent's card failed signature verification, so tasks are not forwarded to it. Check its signature and the trusted key URLs; the card is re-checked automatically."
	DetailRemoteCardKeysUnavailable detail = "A trusted key set for the peer agent's card could not be fetched, so its signature could not be checked. Check the trusted key URLs. Retrying automatically."
	DetailToolSourceUnavailable     detail = "A connector's tools could not be loaded at startup. An unreachable MCP server is retried and its tools load once it responds; otherwise fix the connector, then redeploy the agent."
	DetailEventBrokerUnreachable    detail = "The entrypoint cannot reach the event broker it bridges. Retrying automatically."
)

const StartFailedProblemID = "entrypoint:start"

func HTTPStatusDetail(code int) detail {
	if code < 100 || code > 599 {
		return "remote endpoint returned an invalid HTTP status"
	}
	return detail(fmt.Sprintf("remote endpoint returned HTTP %d", code))
}

type Problem struct {
	ID       string
	Severity Severity
	Detail   string
	Since    time.Time
}

type State struct {
	mu       sync.Mutex
	problems map[string]Problem
}

func (s *State) Set(id string, sev Severity, d detail) {
	s.mu.Lock()
	defer s.mu.Unlock()
	since := time.Now()
	if existing, ok := s.problems[id]; ok {
		since = existing.Since
	}
	if s.problems == nil {
		s.problems = make(map[string]Problem)
	}
	s.problems[id] = Problem{ID: id, Severity: sev, Detail: string(d), Since: since}
}

func (s *State) Clear(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.problems, id)
}

func (s *State) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.problems = nil
}

func (s *State) Snapshot() []Problem {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.problems) == 0 {
		return nil
	}
	out := make([]Problem, 0, len(s.problems))
	for _, p := range s.problems {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Problem) int { return cmp.Compare(a.ID, b.ID) })
	return out
}
