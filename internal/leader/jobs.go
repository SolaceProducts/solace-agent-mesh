// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package leader

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type JobName string

const (
	JobEvidence                       JobName = "evidence"
	JobEvidenceProposalScan           JobName = "evidence_proposal_scan"
	JobEvidenceAsyncIndexes           JobName = "evidence_async_indexes"
	JobEvidenceFindings               JobName = "evidence_findings"
	JobGatewayDataRetention           JobName = "gateway_data_retention"
	JobGatewayTaskRecordCleanup       JobName = "gateway_task_record_cleanup"
	JobGatewayAsyncIndexes            JobName = "gateway_async_indexes"
	JobGatewayLeaderIngress           JobName = "gateway_leader_ingress"
	JobGatewayScheduler               JobName = "gateway_scheduler"
	JobGatewayMCPOAuthExpiry          JobName = "gateway_mcp_oauth_expiry"
	JobPlatformTestAgentPurge         JobName = "platform_test_agent_purge"
	JobPlatformEntrypointBindingSweep JobName = "platform_entrypoint_binding_sweep"
	JobPlatformToolsetDiscoveryExpiry JobName = "platform_toolset_discovery_expiry"
	JobPlatformToolsetUploadCleanup   JobName = "platform_toolset_upload_cleanup"
	JobPlatformSkillDiscoveryExpiry   JobName = "platform_skill_discovery_expiry"
	JobPlatformMCPOAuthStateExpiry    JobName = "platform_mcp_oauth_state_expiry"
	JobPlatformOutboxCleanup          JobName = "platform_outbox_cleanup"
	JobPlatformTokenGovernancePurge   JobName = "platform_token_governance_purge"
	JobPlatformSpendRollup            JobName = "platform_spend_rollup"
	JobPlatformSpendRollupReconcile   JobName = "platform_spend_rollup_reconcile"
	JobPlatformBudgetPeriodCheck      JobName = "platform_budget_period_check"
	JobPlatformBudgetState            JobName = "platform_budget_state"
	JobNotifyInboxPrune               JobName = "notify_inbox_prune"
	JobPlatformReconciler             JobName = "platform_reconciler"
	JobPlatformDeploys                JobName = "platform_deploys"
)

var knownJobs = map[JobName]bool{
	JobEvidence:                       true,
	JobEvidenceProposalScan:           true,
	JobEvidenceAsyncIndexes:           true,
	JobEvidenceFindings:               true,
	JobGatewayDataRetention:           true,
	JobGatewayTaskRecordCleanup:       true,
	JobGatewayAsyncIndexes:            true,
	JobGatewayLeaderIngress:           true,
	JobGatewayScheduler:               true,
	JobGatewayMCPOAuthExpiry:          true,
	JobPlatformTestAgentPurge:         true,
	JobPlatformEntrypointBindingSweep: true,
	JobPlatformToolsetDiscoveryExpiry: true,
	JobPlatformToolsetUploadCleanup:   true,
	JobPlatformSkillDiscoveryExpiry:   true,
	JobPlatformMCPOAuthStateExpiry:    true,
	JobPlatformOutboxCleanup:          true,
	JobPlatformTokenGovernancePurge:   true,
	JobPlatformSpendRollup:            true,
	JobPlatformSpendRollupReconcile:   true,
	JobPlatformBudgetPeriodCheck:      true,
	JobPlatformBudgetState:            true,
	JobNotifyInboxPrune:               true,
	JobPlatformReconciler:             true,
	JobPlatformDeploys:                true,
}

type Job struct {
	Name         JobName
	Every        time.Duration
	OverdueAfter time.Duration
	Run          func(ctx context.Context, term Term) error
}

const NeverOverdue time.Duration = -1

type JobSource interface {
	LeaderJobs() []Job
}

type LogFielder interface {
	LogFields() []any
}

var ErrRegistrationClosed = errors.New("leader: job sources register before the elector initializes")

func validateJobs(jobs []Job) error {
	seen := make(map[JobName]bool, len(jobs))
	var errs []error
	for _, job := range jobs {
		switch {
		case !knownJobs[job.Name]:
			errs = append(errs, fmt.Errorf("leader job %q is not a known job name", job.Name))
		case seen[job.Name]:
			errs = append(errs, fmt.Errorf("leader job %q is registered twice", job.Name))
		case job.Run == nil:
			errs = append(errs, fmt.Errorf("leader job %q has no Run", job.Name))
		case job.Every < 0:
			errs = append(errs, fmt.Errorf("leader job %q has a negative interval", job.Name))
		}
		seen[job.Name] = true
	}
	return errors.Join(errs...)
}
