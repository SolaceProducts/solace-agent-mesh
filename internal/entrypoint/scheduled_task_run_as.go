// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var (
	ErrRunAsDisabled             = errors.New("runAs is not available: it is turned off on this deployment")
	ErrRunAsForbidden            = errors.New("only a super administrator can set runAs or edit a scheduled task that runs as a service account")
	ErrRunAsUnresolvable         = errors.New("runAs cannot be resolved: this gateway has no RBAC identity resolver for service accounts")
	ErrRunAsCreatorNotAuthorized = errors.New("the task's creator no longer holds the authority to run it as a service account")
	ErrRunAsUnknownPrincipal     = errors.New("runAs names no service account that exists and holds a role")
	ErrRunAsPlatformTooOld       = errors.New("runAs is not available: the platform must be upgraded to a version that reports service accounts")
)

const (
	runAsCreatorNotAuthorizedMessage  = "run-as authority lost: the task's creator is no longer a super administrator"
	runAsUnknownPrincipalMessage      = "run-as service account unavailable: it does not exist or holds no role"
	runAsUnresolvableMessage          = "run-as service account cannot be resolved on this gateway"
	runAsDisabledMessage              = "run-as is turned off on this gateway: the task is set to run as a service account, so it was not run"
	runAsPlatformTooOldMessage        = "run-as needs a newer platform: upgrade the platform so it reports service accounts, then the task can run as one"
	runAsDeniedTargetMessage          = "run-as service account not authorized to run the target agent"
	identityUnconfirmedText           = "the identity this run executes as could not be confirmed; waiting to try again"
	platformUnreportedText            = "the platform does not report service accounts yet, so the identity this run executes as could not be confirmed; waiting for the platform to be upgraded"
	identityUnconfirmedTimeoutMessage = "the identity this run executes as could not be confirmed before the run timed out"
	identityResolutionFailedMessage   = "identity resolution failed"
	noOwnerMessage                    = "scheduled task has no owner"
)

func (s *ScheduledTaskService) RunAsEnabled() bool {
	reg := s.core.Services().FeatureRegistry()
	return reg != nil && reg.IsEnabled(features.KeyScheduledTaskRunAs)
}

func storedRunAs(task *store.ScheduledTask) string {
	if task.RunAs == nil {
		return ""
	}
	return *task.RunAs
}

func stampedRunAs(run *store.ScheduledTaskExecution) string {
	if run.RunAs == nil {
		return ""
	}
	return *run.RunAs
}

func (s *ScheduledTaskService) runCaller(ctx context.Context, task *store.ScheduledTask, runAs string, lookupDown error) (*auth.Subject, error) {
	switch {
	case runAs == "":
		return s.ownerCaller(ctx, task)
	case !s.RunAsEnabled():
		return nil, ErrRunAsDisabled
	case lookupDown != nil:
		return nil, lookupDown
	default:
		return s.runAsCaller(ctx, task, runAs)
	}
}

func runWireUserID(task *store.ScheduledTask, runAs string) string {
	return cmp.Or(runAs, task.ResolveOwnerUserID())
}

func (s *ScheduledTaskService) runAsCaller(ctx context.Context, task *store.ScheduledTask, runAs string) (*auth.Subject, error) {
	resolver := s.core.IdentityResolver()
	runAsResolver, ok := resolver.(RunAsResolver)
	if !ok {
		return nil, classifiedAs(ErrIdentityUnauthorized, ErrRunAsUnresolvable)
	}
	creator, err := resolver.ResolveIdentity(ctx, task.CreatedBy)
	switch {
	case errors.Is(err, ErrIdentityUnauthorized):
		return nil, classifiedAs(ErrIdentityUnauthorized, classifiedAs(ErrRunAsCreatorNotAuthorized, err))
	case err != nil:
		return nil, err
	case !rbac.IsSuperAdmin(creator.Scopes()):
		return nil, classifiedAs(ErrIdentityUnauthorized, ErrRunAsCreatorNotAuthorized)
	}
	caller, err := runAsResolver.ResolveRunAs(ctx, runAs)
	if errors.Is(err, ErrIdentityUnauthorized) {
		return nil, classifiedAs(ErrRunAsUnknownPrincipal, err)
	}
	return caller, err
}

func classifiedAs(kind, cause error) error {
	return fmt.Errorf("%w: %w", kind, cause)
}

func deniedTargetMessage(runAs string) string {
	if runAs == "" {
		return ownerDeniedTargetMessage
	}
	return runAsDeniedTargetMessage
}

func ownerRefusalMessage(err error) string {
	switch {
	case errors.Is(err, auth.ErrOwnerDeactivated):
		return ownerDeactivatedMessage
	case errors.Is(err, auth.ErrOwnerUnresolved):
		return ownerUnresolvedMessage
	default:
		return ownerUnauthorizedMessage
	}
}

func ownerRefusalAttrs(err error) []any {
	var refusal *auth.OwnerRefusal
	if !errors.As(err, &refusal) {
		return nil
	}
	return []any{
		"resolution", string(refusal.Resolution),
		"refusalReason", refusal.RefusalReason,
		"rolesSource", string(refusal.RolesSource),
	}
}

func creatorRefusalMessage(err error) string {
	if errors.Is(err, auth.ErrOwnerDeactivated) || errors.Is(err, auth.ErrOwnerUnresolved) {
		return ownerRefusalMessage(err)
	}
	return runAsCreatorNotAuthorizedMessage
}

func runRefusalMessage(err error) string {
	switch {
	case errors.Is(err, ErrRunAsCreatorNotAuthorized):
		return creatorRefusalMessage(err)
	case errors.Is(err, ErrRunAsUnknownPrincipal):
		return runAsUnknownPrincipalMessage
	case errors.Is(err, ErrRunAsUnresolvable):
		return runAsUnresolvableMessage
	default:
		return ownerUnauthorizedMessage
	}
}

func (s *ScheduledTaskService) CheckRunAsWrite(ctx context.Context, author *auth.Subject, runAs string) error {
	if !s.RunAsEnabled() {
		return ErrRunAsDisabled
	}
	if author == nil || !rbac.IsSuperAdmin(author.Scopes()) {
		return ErrRunAsForbidden
	}
	if err := auth.CheckScheduledRunAs(runAs); err != nil {
		return err
	}
	runAsResolver, ok := s.core.IdentityResolver().(RunAsResolver)
	if !ok {
		return ErrRunAsUnresolvable
	}
	_, err := runAsResolver.ResolveRunAs(ctx, runAs)
	if errors.Is(err, auth.ErrSystemUserRowNotReported) {
		_, err = runAsResolver.ResolveRunAs(ctx, runAs)
	}
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrIdentityUnauthorized):
		return fmt.Errorf("%w: %q", ErrRunAsUnknownPrincipal, runAs)
	case errors.Is(err, auth.ErrSystemUserRowNotReported):
		return ErrRunAsPlatformTooOld
	}
	return err
}

func CheckRunAsTaskEdit(author *auth.Subject, task *store.ScheduledTask) error {
	if task.RunAs == nil || *task.RunAs == "" {
		return nil
	}
	if author == nil || !rbac.IsSuperAdmin(author.Scopes()) {
		return ErrRunAsForbidden
	}
	return nil
}
