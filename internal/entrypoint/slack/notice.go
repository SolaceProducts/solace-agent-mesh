// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	goslack "github.com/slack-go/slack"
)

const gatewayNoticeEventType = "sam_gateway_notice"

const noticeKindPayloadKey = "kind"

type noticeKind string

const (
	noticeBudget  noticeKind = "budget"
	noticeRefusal noticeKind = "refusal"
	noticeError   noticeKind = "error"
	noticeStatus  noticeKind = "status"
	noticeInfo    noticeKind = "info"
)

var knownNoticeKinds = map[noticeKind]bool{
	noticeBudget: true, noticeRefusal: true, noticeError: true, noticeStatus: true, noticeInfo: true,
}

func noticeKindForReason(reason string) noticeKind {
	switch reason {
	case taxonomy.RejectBudgetExhausted, taxonomy.RejectBudgetUnconfirmed:
		return noticeBudget
	case taxonomy.RejectForbidden, taxonomy.RejectAgentNotFound:
		return noticeRefusal
	}
	return noticeError
}

func noticeKindForSubmitFailure(err error) noticeKind {
	return noticeKindForReason(entrypoint.SubmitFailureReason(err))
}

func noticeMetadata(kind noticeKind) goslack.MsgOption {
	if !knownNoticeKinds[kind] {
		return goslack.MsgOptionCompose()
	}
	return goslack.MsgOptionMetadata(goslack.SlackMetadata{
		EventType:    gatewayNoticeEventType,
		EventPayload: map[string]any{noticeKindPayloadKey: string(kind)},
	})
}

func (a *SlackGatewayAdapter) ownNoticeKind(msg *goslack.Message) (noticeKind, bool) {
	if !a.isOwnMessage(msg) || msg.Metadata.EventType != gatewayNoticeEventType {
		return "", false
	}
	kind, _ := msg.Metadata.EventPayload[noticeKindPayloadKey].(string)
	if !knownNoticeKinds[noticeKind(kind)] {
		return noticeError, true
	}
	return noticeKind(kind), true
}
