// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import "context"

type ctxKey int

const identityKey ctxKey = iota

func ContextWithSubject(ctx context.Context, id *Subject) context.Context {
	return context.WithValue(ctx, identityKey, id)
}

func SubjectFromContext(ctx context.Context) (*Subject, bool) {
	id, ok := ctx.Value(identityKey).(*Subject)
	return id, ok
}

func UserIDFromContext(ctx context.Context) (userID, email string) {
	id, ok := ctx.Value(identityKey).(*Subject)
	if !ok || id == nil {
		return "", ""
	}
	return id.userID, id.email
}
