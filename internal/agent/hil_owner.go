// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type hilDelivery int

const (
	hilUnmatched hilDelivery = iota
	hilDelivered
	hilRefused
	hilNotRecorded
	hilUnchecked
)

type hilSender struct {
	userID        string
	notifyService bool
}

func hilSenderUser(userID string) hilSender { return hilSender{userID: userID} }

var hilSenderNotifyService = hilSender{notifyService: true}

func (s hilSender) mayAnswer(ownerUserID string) bool {
	return s.notifyService || artifact.SameOwner(ownerUserID, s.userID)
}

func (s hilSender) mayAnswerRequest(p pendingHILRequest) bool {
	if p.approverSet {
		return s.notifyService
	}
	return s.mayAnswer(p.ownerUserID)
}

func warnControlResponseRefused(logger *slog.Logger, kind a2a.SignalType, sender hilSender) {
	logger.Warn("control response refused: sender does not own the paused task",
		"responseType", string(kind),
		"userID", sender.userID,
	)
}
