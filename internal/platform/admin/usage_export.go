// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/usage"
)

func UsageSummaryDTO(s usage.Summary) UsageSummaryResponse { return toUsageSummaryResponse(s) }

func UsageRowDTOs(rows []usage.Row) []UsageRowResponse { return toUsageRows(rows) }

func UsageUserRowDTOs(rows []usage.Row) []UsageUserRowResponse { return toUsageUserRows(rows) }

func UsageModelRowDTOs(rows []usage.Row) []UsageModelRowResponse { return toUsageModelRows(rows) }

func UsageSubjectDTO(s usage.Subject) UsageSubjectResponse { return toUsageSubjectResponse(s) }

func UsageModelDTO(s usage.Subject) UsageModelResponse { return toUsageModelResponse(s) }

func UsageWorkflowDTO(wf usage.Workflow) UsageWorkflowResponse { return toUsageWorkflowResponse(wf) }

func UsageUserDTO(u usage.User) UsageUserResponse { return toUsageUserResponse(u, UsageScopeTenant) }

func UsageSelfDTO(s usage.Self) UsageSelfResponse { return toUsageSelfResponse(s) }

func UsageSavingsDTO(s usage.Savings) UsageSavingsResponse {
	return toUsageSavingsResponse(s, UsageScopeTenant)
}

func UsageSelfSavingsDTO(s usage.Savings) UsageSavingsResponse {
	return toUsageSavingsResponse(s, UsageScopeSelf)
}

func ParseUsageDate(text string) (time.Time, error) { return parseUsageDate(text) }

func AuditUsageRead(ctx context.Context, logger *slog.Logger, route, subjectID string, attrs ...any) {
	actor, actorUserID := "", ""
	if id, ok := auth.SubjectFromContext(ctx); ok && id != nil {
		actor, actorUserID = id.UserID(), id.PlatformUserID()
	}
	if subjectID != "" {
		attrs = append(attrs, "subjectID", subjectID)
	}
	withAuditCredential(ctx, logger).Info(msgUsageRead,
		append([]any{"actor", actor, "actorUserID", actorUserID, "route", route, "crossUser", true}, attrs...)...)
}

func UsageReadCause(err error) error {
	cause := withoutConnectDetail(platformDatabase, err)
	if dbutil.IsBusy(err) {
		return fmt.Errorf("%w: %w", errRetryable, cause)
	}
	return cause
}
