// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/budgetstate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type budgetAdminDirectory struct {
	users    *store.UsersRepo
	composer *rbac.Composer
	logger   *slog.Logger
}

func newBudgetAdminDirectory(users *store.UsersRepo, composer *rbac.Composer, logger *slog.Logger) *budgetAdminDirectory {
	return &budgetAdminDirectory{users: users, composer: composer, logger: withLoggerName(logger, "budget-alert-admins")}
}

func registerBudgetAdmins(core *platpkg.PlatformCore, deps *RouteDeps, users *store.UsersRepo, composer *rbac.Composer, logger *slog.Logger) {
	if deps.IsRBACAuthority {
		core.SetBudgetAdmins(newBudgetAdminDirectory(users, composer, logger))
	}
}

func (d *budgetAdminDirectory) BudgetAdmins(ctx context.Context) ([]budgetstate.Recipient, error) {
	users, err := d.users.ListAllUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	var admins []budgetstate.Recipient
	for i := range users {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		u := &users[i]
		if !u.Active || u.ProvisionedVia == rbac.ProvisionedViaSystem {
			continue
		}
		identity, gctx, claimsErr := userLookupContext(u)
		if claimsErr != nil {
			d.logger.Warn("user claims unparseable; budget alerts see only their other roles", "userId", u.ID, "error", claimsErr)
		}
		res, err := d.composer.Lookup(identity, "", gctx)
		if err != nil {
			return nil, fmt.Errorf("composer lookup: %w", err)
		}
		if rbac.CheckAnyScope(res.Scopes, rbac.BudgetAdminScopes()) {
			admins = append(admins, budgetstate.Recipient{
				PlatformUserID:   u.ID,
				LoginID:          cmp.Or(derefStr(u.Email), u.Sub),
				CanReviewBudgets: rbac.CheckScopes(res.Scopes, []string{rbac.ScopeBudgetRead}),
			})
		}
	}
	return admins, nil
}
