// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestTeamsHandleHILSubmit_AnswersAsTheChannelPolicyPrincipal(t *testing.T) {
	const clicker = "29:clicker"
	realUser := "teams:" + clicker
	for name, tc := range map[string]struct {
		conversationType string
		want             string
	}{
		"channel":              {ConversationTypeChannel, rbac.SystemUserPrincipal("ops")},
		"group chat":           {ConversationTypeGroupChat, rbac.SystemUserPrincipal("ops")},
		"personal chat":        {ConversationTypePersonal, realUser},
		"unknown conversation": {"meeting", rbac.SystemUserPrincipal("ops")},
	} {
		t.Run(name, func(t *testing.T) {
			a, brk, _ := newHILTeamsAdapter(t)
			a.cfg.RunAs = "ops"
			q := subscribeToAgent(t, brk, "teams-runas-"+tc.conversationType)

			a.handleHILSubmit(clickActivity(t, a, "req-"+tc.conversationType, "task-"+tc.conversationType, "conv-1", tc.conversationType, clicker))

			msg := receiveWithin(t, brk, q, 2*time.Second)
			if msg == nil {
				t.Fatal("the answer was not forwarded")
			}
			if got := a2a.DecodeUserProperties(msg.UserProperties).UserID; got != tc.want {
				t.Errorf("answered as %q, want %q", got, tc.want)
			}
		})
	}
}
