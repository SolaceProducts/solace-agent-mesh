// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func seedCard(w *WorkflowInstance, card a2a.AgentCard) {
	w.cardDirectory().Registry().Observe(card, "")
}

func unseedCard(w *WorkflowInstance, name string) {
	w.cardDirectory().Registry().Delete(name)
}

func addCardExtensions(w *WorkflowInstance, name string, exts ...a2a.AgentExtension) {
	card := *w.cardDirectory().Registry().Get(name)
	card.Capabilities.Extensions = append(slices.Clone(card.Capabilities.Extensions), exts...)
	seedCard(w, card)
}
