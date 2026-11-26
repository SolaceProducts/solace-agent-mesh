// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

type ControlAuth struct {
	auditLogger auth.AuditLogger
	logger      *slog.Logger
}

func NewControlAuth(auditLogger auth.AuditLogger, logger *slog.Logger) *ControlAuth {
	return &ControlAuth{
		auditLogger: auditLogger,
		logger:      logger,
	}
}

func (ca *ControlAuth) AuthorizeControl(ctx context.Context, identity *auth.Identity, method string, appName string) error {
	if identity == nil {
		return fmt.Errorf("no identity provided")
	}

	requiredScope := buildControlScope(method, appName)
	allowed := CheckScopes(identity.Scopes, []string{requiredScope})

	if ca.auditLogger != nil {
		ca.auditLogger.LogControlAccess(ctx, auth.ControlAccessEvent{
			UserID:  identity.UserID,
			Method:  method,
			AppName: appName,
			Allowed: allowed,
		})
	}

	if !allowed {
		ca.logger.Warn("control access denied",
			"userID", identity.UserID,
			"method", method,
			"appName", appName,
			"requiredScope", requiredScope,
		)
		return fmt.Errorf("access denied: insufficient scopes for control %s %s", method, appName)
	}
	return nil
}

func buildControlScope(method, appName string) string {
	action := methodToAction(method)

	if appName == "" {
		return fmt.Sprintf("control:apps:%s", action)
	}
	return fmt.Sprintf("control:apps/%s:%s", appName, action)
}

func methodToAction(method string) string {
	switch method {
	case "get":
		return "read"
	case "post":
		return "create"
	case "put", "patch":
		return "update"
	case "delete":
		return "delete"
	default:
		return "manage"
	}
}

var _ auth.ControlAuthorizer = (*ControlAuth)(nil)
