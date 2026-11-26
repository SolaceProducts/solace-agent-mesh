// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestTerminalCallbackPanicStillEndsTheReply(t *testing.T) {
	for name, terminate := range map[string]func(a *Adapter, ctx context.Context, taskID string){
		"final response": func(a *Adapter, ctx context.Context, taskID string) { a.OnFinalResponse(ctx, taskID, nil, nil) },
		"error":          func(a *Adapter, ctx context.Context, taskID string) { a.OnError(ctx, taskID, nil, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			chat := newFakeChat()
			a := newTestAdapter(t, store.TestDB(t), chat)
			submitForTest(t, a, tid("task-p"), dmMeta())
			writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
			status := writes[0].name

			terminate(a, t.Context(), tid("task-p"))

			chat.waitFor(t, func(w []fakeWrite, texts []string) bool {
				cleared := slices.ContainsFunc(w, func(x fakeWrite) bool { return x.name == status && x.replacedCards && x.cards == 0 })
				return cleared && slices.Contains(texts, msgAgentError)
			})
		})
	}
}
