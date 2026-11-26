// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type identityRequest struct {
	field string
	verb  string
}

var (
	grantIdentity      = identityRequest{field: "subject", verb: "grant"}
	assignmentIdentity = identityRequest{field: "identity", verb: "assign"}
)

func (r identityRequest) resolveIdentity(ctx context.Context, users *store.UsersRepo, identityType, value, issuer string) (string, error) {
	switch identityType {
	case store.PendingSubjectSub:
		if issuer == "" {
			return "", &ValidationError{
				Message: fmt.Sprintf("Issuer is required for a sub %s", r.field),
				Details: map[string][]string{r.field + ".issuer": {fmt.Sprintf("Required when %s.type is sub", r.field)}},
			}
		}
		u, _, err := users.GetByIssuerSub(ctx, issuer, value)
		if err != nil {
			return "", fmt.Errorf("resolve %s by sub: %w", r.field, err)
		}
		if u == nil {
			return "", nil
		}
		return u.ID, nil
	case store.PendingSubjectEmail:
		ids, err := users.UserIDsByEmail(ctx, value)
		if err != nil {
			return "", fmt.Errorf("resolve %s by email: %w", r.field, err)
		}
		switch len(ids) {
		case 0:
			return "", nil
		case 1:
			return ids[0], nil
		}
		return "", &ValidationError{
			Message: fmt.Sprintf("Email matches %d users; %s by sub or resolve the duplicate", len(ids), r.verb),
			Details: map[string][]string{r.field + ".value": {"Matches multiple users"}},
		}
	}
	return "", &ValidationError{Message: fmt.Sprintf("Unknown %s type %q", r.field, identityType)}
}
