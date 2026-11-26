// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"errors"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

var ErrNotTaskOwner = errors.New("answering user does not own the task")

func (g *GatewayCore) trackedTaskOwner(taskID string) string {
	if info := g.tasks.Get(taskID); info != nil {
		return info.UserID
	}
	return ""
}

func (g *GatewayCore) MayAnswerPrompt(taskID, answerUserID string) bool {
	owner := g.trackedTaskOwner(taskID)
	return owner == "" || artifact.SameOwner(owner, answerUserID)
}

type ChatPlace struct {
	Conversation string
	User         string
}

func (p ChatPlace) complete() bool { return p.Conversation != "" && p.User != "" }

func (g *GatewayCore) PromptAnswerUserID(taskID, clickerUserID string, asked, clicked ChatPlace) string {
	if owner := g.trackedTaskOwner(taskID); owner != "" && asked.complete() && asked == clicked {
		return owner
	}
	return clickerUserID
}
