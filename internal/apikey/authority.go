// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apikey

import (
	"context"
	"errors"
)

type Resolution struct {
	KeyID string

	OwnerID string

	OwnerSub string

	Scopes []string
}

var ErrInvalidKey = errors.New("apikey: credential is not valid")

var ErrAuthorityUnavailable = errors.New("apikey: authority unavailable")

var ErrAuthorizationModeDisabled = errors.New("apikey: authorization mode disabled")

var ErrNotEnabled = errors.New("apikey: api keys are not enabled")

type Authority interface {
	Validate(ctx context.Context, keyHash string) (Resolution, error)
}
