// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const maxToolAuthRegistrationFieldLen = 1024

const maxToolAuthTargetAgentLen = 128

const maxToolAuthLoggedHeaderLen = 128

const toolAuthRegisterQueueQuotaMB = 256

const toolAuthStateWriteTimeout = 5 * time.Second

var toolAuthTargetAgentPattern = regexp.MustCompile(`^[\w\-\.]+$`)

func shortHeader(v string) string {
	return strutil.Truncate(v, maxToolAuthLoggedHeaderLen, "…")
}

func (g *GatewayCore) StartToolAuthRegisterConsumer(ctx context.Context, logger *slog.Logger) {
	if g == nil || g.svc == nil {
		return
	}
	brk := g.svc.Broker()
	if brk == nil {
		return
	}
	if logger == nil {
		logger = g.logger
	}
	queueName := a2a.QueueName(g.Namespace(), "gdk", "toolauth_register")
	q, err := brk.CreateQueue(queueName, broker.QueueOptions{
		Named:         true,
		Exclusive:     false,
		MaxRedelivery: 3,
		QuotaMB:       toolAuthRegisterQueueQuotaMB,
		RespectTTL:    true,
	})
	if err != nil {
		logger.Error("tool auth register: create queue failed; this core will not consume registrations", "queue", queueName, "error", err)
		return
	}
	if err := brk.Subscribe(q, a2a.ToolAuthRegisterSubscription(g.Namespace())); err != nil {
		logger.Error("tool auth register: subscribe failed; this core will not consume registrations", "queue", queueName, "error", err)
		return
	}
	if g.db == nil {
		logger.Warn("tool auth register: no gateway database configured; seeded state is process-local and a callback served by another replica will fail",
			"queue", queueName)
	}
	g.loopWg.Add(1)
	go func() {
		defer g.loopWg.Done()
		g.consumeToolAuthRegister(ctx, brk, q, logger)
	}()
	logger.Info("tool auth register consumer started", "queue", queueName)
}

func (g *GatewayCore) consumeToolAuthRegister(ctx context.Context, brk broker.Broker, q broker.Queue, logger *slog.Logger) {
	bo := backoff.Exponential{
		Initial: 50 * time.Millisecond,
		Max:     5 * time.Second,
		Jitter:  0.2,
	}
	for {
		msg, err := brk.Receive(ctx, q)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, broker.ErrQueueNotFound) {
				logger.Error("tool auth register: receiver unavailable; platform-seeded OAuth flows disabled for this process",
					"queue", q.Name(), "error", err)
				return
			}
			logger.Warn("tool auth register: receive error; backing off", "error", err, "backoff", bo.Current())
			if waitErr := bo.Wait(ctx); waitErr != nil {
				return
			}
			continue
		}
		bo.Reset()
		if msg == nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		g.handleToolAuthRegister(ctx, msg, logger)
	}
}

func (g *GatewayCore) handleToolAuthRegister(ctx context.Context, msg *broker.Message, logger *slog.Logger) {
	props := a2a.DecodeUserProperties(msg.UserProperties)
	var reg a2a.ToolAuthStateRegistration

	defer msg.Ack()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("tool auth register: panic handling registration",
				"panic", shortHeader(fmt.Sprint(r)),
				"stateFP", toolAuthStateFP(reg.State), "traceID", shortHeader(props.TraceID))
		}
	}()

	if err := json.Unmarshal(msg.Payload, &reg); err != nil {
		logger.Warn("tool auth register: malformed registration; dropping",
			"clientID", shortHeader(props.ClientID), "traceID", shortHeader(props.TraceID),
			"payloadLen", len(msg.Payload), "error", err)
		return
	}
	if err := validateToolAuthRegistration(&reg); err != nil {
		logger.Warn("tool auth register: invalid registration; dropping", "reason", err.Error(),
			"stateFP", toolAuthStateFP(reg.State), "taskID", shortHeader(reg.TaskID),
			"clientID", shortHeader(props.ClientID), "traceID", shortHeader(props.TraceID))
		return
	}

	authState := toolAuthStateFromRegistration(&reg, props.ClientID)
	ttl := resolveToolAuthRegistrationTTL(reg.TTLMs)
	if reg.TTLMs > maxToolAuthStateTTL.Milliseconds() {
		logger.Warn("tool auth register: requested TTL above the maximum; clamped",
			"stateFP", toolAuthStateFP(reg.State), "requestedTtlMs", reg.TTLMs, "ttl", ttl)
	}

	writeCtx, cancelWrite := context.WithTimeout(context.WithoutCancel(ctx), toolAuthStateWriteTimeout)
	defer cancelWrite()

	if err := g.putToolAuthStateTTL(writeCtx, reg.State, authState, ttl); err != nil {
		logger.Error("tool auth register: failed to seed tool auth state",
			"stateFP", toolAuthStateFP(reg.State), "agentName", reg.TargetAgent, "taskID", reg.TaskID,
			"traceID", shortHeader(props.TraceID), "error", err)
		return
	}

	logger.Info("seeded tool auth state from registration",
		"stateFP", toolAuthStateFP(reg.State),
		"toolType", authState.ToolType,
		"agentName", reg.TargetAgent,
		"taskID", reg.TaskID,
		"userFP", strutil.PIIFingerprint(reg.UserID),
		"credentialKey", reg.CredentialKey,
		"traceID", shortHeader(props.TraceID),
		"ttl", ttl,
	)
}

func validateToolAuthRegistration(reg *a2a.ToolAuthStateRegistration) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"state", reg.State},
		{"target_agent", reg.TargetAgent},
		{"task_id", reg.TaskID},
		{"user_id", reg.UserID},
		{"client_id", reg.ClientID},
		{"function_call_id", reg.FunctionCallID},
		{"credential_key", reg.CredentialKey},
		{"code_verifier", reg.CodeVerifier},
	} {
		if len(field.value) > maxToolAuthRegistrationFieldLen {
			return fmt.Errorf("field %s exceeds maximum length: %d > %d",
				field.name, len(field.value), maxToolAuthRegistrationFieldLen)
		}
	}
	switch {
	case reg.State == "":
		return errors.New("missing state")
	case reg.TargetAgent == "":
		return errors.New("missing target_agent")
	case reg.TaskID == "":
		return errors.New("missing task_id")
	case reg.UserID == "":
		return errors.New("missing user_id")
	case reg.CredentialKey == "":
		return errors.New("missing credential_key")
	}
	if len(reg.TargetAgent) > maxToolAuthTargetAgentLen {
		return fmt.Errorf("field target_agent exceeds maximum length: %d > %d",
			len(reg.TargetAgent), maxToolAuthTargetAgentLen)
	}
	if !toolAuthTargetAgentPattern.MatchString(reg.TargetAgent) {
		return errors.New("target_agent is not a valid agent name")
	}
	return nil
}

func toolAuthStateFromRegistration(reg *a2a.ToolAuthStateRegistration, brokerClientID string) *ToolAuthState {
	clientID := reg.ClientID
	if clientID == "" {
		clientID = brokerClientID
	}
	return &ToolAuthState{
		ToolType:       toolAuthTypeForCredentialKey(reg.CredentialKey),
		TargetAgent:    reg.TargetAgent,
		TaskID:         reg.TaskID,
		UserID:         reg.UserID,
		ClientID:       clientID,
		FunctionCallID: reg.FunctionCallID,
		CredentialKey:  reg.CredentialKey,
		CodeVerifier:   reg.CodeVerifier,
	}
}

func resolveToolAuthRegistrationTTL(ttlMs int64) time.Duration {
	switch {
	case ttlMs <= 0:
		return mcpAuthStateTTL
	case ttlMs > maxToolAuthStateTTL.Milliseconds():
		return maxToolAuthStateTTL
	case ttlMs < minToolAuthStateTTL.Milliseconds():
		return minToolAuthStateTTL
	}
	return time.Duration(ttlMs) * time.Millisecond
}
