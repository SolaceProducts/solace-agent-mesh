// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"errors"
	"net"

	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const whatsappGatewayName = "WhatsAppGateway"

const metricWhatsAppFeedback = "whatsapp_feedback"

const (
	fbOutcomeUp                  = "up"
	fbOutcomeDown                = "down"
	fbOutcomeReason              = "reason"
	fbOutcomeIgnoredUnclassified = "ignored_unclassified"
	fbOutcomeIgnoredUntracked    = "ignored_untracked"
	fbOutcomeRemovedNoop         = "removed_noop"
	fbOutcomeRevoked             = "revoked"
	fbOutcomeConflict            = "conflict"
	fbOutcomeUnchanged           = "unchanged"
	fbOutcomeDropped             = "dropped"
)

const (
	opSendMessage = "send_message"
	opUploadMedia = "upload_media"
)

const (
	errTypeRateLimit  = "rate_limit"
	errTypeAuth       = "auth_error"
	errTypeClient     = "4xx_error"
	errTypeServer     = "5xx_error"
	errTypeTimeout    = "timeout"
	errTypeConnection = "connection_error"
	errTypeCanceled   = "canceled"
	errTypeLocal      = "error"
	errTypePanic      = "panic"
)

func outboundErrorType(err error) string {
	if err == nil {
		return ""
	}
	switch graphFailureClassOf(err) {
	case classRateLimited:
		return errTypeRateLimit
	case classCredentials, classUnverified:
		return errTypeAuth
	case classPermanent:
		return errTypeClient
	case classTransient:
		return errTypeServer
	case classCanceled:
		return errTypeCanceled
	case classLocal:
		return errTypeLocal
	}
	if isTimeoutError(err) {
		return errTypeTimeout
	}
	return errTypeConnection
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func sendErrorType(err error) string {
	if err == nil || errors.Is(err, errRequestAccepted) {
		return ""
	}
	return outboundErrorType(err)
}

func (a *Adapter) meter() runtime.Metrics {
	return runtime.MetricsOrNoop(a.metrics)
}

var inboundMessageTypes = map[string]struct{}{
	"text":            {},
	"image":           {},
	"audio":           {},
	"video":           {},
	"document":        {},
	"sticker":         {},
	"location":        {},
	"contacts":        {},
	"interactive":     {},
	"button":          {},
	"order":           {},
	"reaction":        {},
	"system":          {},
	"ephemeral":       {},
	"request_welcome": {},
	"unknown":         {},
}

func inboundTypeLabel(msgType string) string {
	if _, ok := inboundMessageTypes[msgType]; ok {
		return msgType
	}
	if msgType == "" {
		return "none"
	}
	return "other"
}
