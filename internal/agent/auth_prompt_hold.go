// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"log/slog"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type heldAuthPrompts struct {
	send   func(signal any) error
	logger *slog.Logger

	mu   sync.Mutex
	held []*a2a.AuthenticationRequiredData
}

func holdAuthPrompts(toolCtx *tool.Context, logger *slog.Logger) (*tool.Context, *heldAuthPrompts) {
	h := &heldAuthPrompts{logger: logger}
	if toolCtx == nil || toolCtx.SendSignal == nil {
		return toolCtx, h
	}
	h.send = toolCtx.SendSignal
	held := *toolCtx
	held.SendSignal = h.capture
	return &held, h
}

func (h *heldAuthPrompts) capture(signal any) error {
	prompt, isAuthPrompt := signal.(*a2a.AuthenticationRequiredData)
	if !isAuthPrompt {
		return h.send(signal)
	}
	h.mu.Lock()
	h.held = append(h.held, prompt)
	h.mu.Unlock()
	return nil
}

func (h *heldAuthPrompts) take() []*a2a.AuthenticationRequiredData {
	h.mu.Lock()
	defer h.mu.Unlock()
	held := h.held
	h.held = nil
	return held
}

func (h *heldAuthPrompts) release() {
	for _, prompt := range h.take() {
		if err := h.send(prompt); err != nil {
			h.logger.Warn("held authentication prompt could not be sent; the task waits until its auth timeout",
				"credentialKey", prompt.CredentialKey, "toolCallID", prompt.AuthRequestFunctionCallID, "error", err)
		}
	}
}

func (h *heldAuthPrompts) discard() {
	if held := h.take(); len(held) > 0 {
		h.logger.Debug("discarded held authentication prompts of a task cancelled before it paused", "count", len(held))
	}
}
