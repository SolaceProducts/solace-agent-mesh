// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func senderIdentity(waID, profileName string) *auth.Subject {
	return auth.ForWhatsAppSenderIdentity(waID, profileName)
}

const (
	principalSourceRunAs        = "run_as"
	principalSourceMatchedEmail = "matched_email"
	principalSourceSender       = "sender"
)

func runAsPrincipalOrEmpty(cfg *WhatsAppConfig) string {
	if cfg.RunAs == "" {
		return ""
	}
	return rbac.SystemUserPrincipal(cfg.RunAs)
}
