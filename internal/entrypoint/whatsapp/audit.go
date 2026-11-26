// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const auditMsg = "message audit"

const auditDigestCap = 64 * 1024

func contentDigest(data []byte) string {
	if len(data) > auditDigestCap {
		data = data[:auditDigestCap]
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

type inboundAudit struct {
	userID      string
	senderID    string
	agentName   string
	taskID      string
	traceID     string
	sessionID   string
	artifactRef string
	contentLen  int
	contentSHA  string

	principalSource string
}

func (a *Adapter) auditInbound(msg *inboundMessage, rec inboundAudit) {
	fields := []any{
		"direction", "inbound",
		"wamid", msg.ID,
		"messageType", msg.Type,
		"userFP", strutil.PIIFingerprint(rec.userID),
		"agentName", rec.agentName,
		"taskID", rec.taskID,
		"traceID", rec.traceID,
		"sessionID", rec.sessionID,
		"artifactRef", rec.artifactRef,
		"contentLen", rec.contentLen,
		"contentSHA", rec.contentSHA,
	}
	if rec.principalSource != principalSourceSender {
		fields = append(fields, "senderFP", strutil.PIIFingerprint(rec.senderID))
	}
	a.logger.Debug(auditMsg, fields...)
}

const (
	refusalRateLimited             = "sender_rate_limited"
	refusalTaskLimit               = "sender_task_limit"
	refusalUploadsDisabled         = "uploads_disabled"
	refusalSenderUnidentified      = "sender_unidentified"
	refusalSenderNotUsable         = "sender_not_usable"
	refusalSenderNoValue           = "sender_no_value"
	refusalIdentityGateUnavailable = "identity_gate_unavailable"
)

func (a *Adapter) auditRefused(msg *inboundMessage, reason string) {
	a.logger.Debug(auditMsg,
		"direction", "inbound",
		"wamid", msg.ID,
		"messageType", msg.Type,
		"userFP", strutil.PIIFingerprint("whatsapp:"+msg.senderID()),
		"disposition", "refused",
		"refusalReason", reason,
	)
}

func firstArtifactRef(parts []a2a.Part) string {
	for i := range parts {
		if parts[i].Kind != "file" || parts[i].File == nil || parts[i].File.URI == "" {
			continue
		}
		filename, version, ok := entrypoint.ParseArtifactURI(parts[i].File.URI)
		if !ok {
			return ""
		}
		if version != nil {
			return filename + "?version=" + strconv.Itoa(*version)
		}
		return filename
	}
	return ""
}

type outboundAudit struct {
	wamid      string
	msgType    string
	recipient  string
	agentName  string
	taskID     string
	traceID    string
	contentRef string
	contentLen int
	contentSHA string
	sendErr    error
}

func (a *Adapter) auditOutboundRecord(rec outboundAudit) {
	status := "sent"
	metaErrorCode := 0
	if rec.sendErr != nil {
		status = "failed"
		if ge, ok := errors.AsType[*graphAPIError](rec.sendErr); ok {
			metaErrorCode = ge.GraphErr.Code
		}
	}
	a.logger.Debug(auditMsg,
		"direction", "outbound",
		"wamid", rec.wamid,
		"messageType", rec.msgType,
		"userFP", strutil.PIIFingerprint("whatsapp:"+rec.recipient),
		"agentName", rec.agentName,
		"taskID", rec.taskID,
		"traceID", rec.traceID,
		"contentRef", rec.contentRef,
		"contentLen", rec.contentLen,
		"contentSHA", rec.contentSHA,
		"deliveryStatus", status,
		"metaErrorCode", metaErrorCode,
	)
}

func summarizeParts(parts []a2a.Part) (int, string) {
	h := sha256.New()
	total := 0
	for i := range parts {
		switch {
		case parts[i].Kind == "text":
			h.Write([]byte(parts[i].Text))
			total += len(parts[i].Text)
		case parts[i].Kind == "file" && parts[i].File != nil && parts[i].File.Bytes != "":
			b := parts[i].File.Bytes
			if len(b) > auditDigestCap {
				b = b[:auditDigestCap]
			}
			h.Write([]byte(b))
			total += base64.StdEncoding.DecodedLen(len(parts[i].File.Bytes))
		}
	}
	return total, hex.EncodeToString(h.Sum(nil)[:8])
}

func (a *Adapter) auditHILAnswer(p *pendingHIL, msg *inboundMessage) {
	sender := senderIdentity(msg.senderID(), "")
	identity, swapped := auth.ForWhatsAppSender(p.authorized, a.cfg.RunAs)
	source := p.authorizedSource
	switch {
	case swapped:
		source = principalSourceRunAs
	case identity == nil:
		identity, source = sender, principalSourceSender
	}
	fields := []any{
		"direction", "inbound",
		"answeredPrompt", true,
		"wamid", msg.ID,
		"messageType", msg.Type,
		"userFP", strutil.PIIFingerprint(identity.UserID()),
		"agentName", p.agentName,
		"taskID", p.taskID,
	}
	if source != principalSourceSender {
		fields = append(fields, "senderFP", strutil.PIIFingerprint(sender.UserID()))
	}
	a.logger.Debug(auditMsg, fields...)
}
