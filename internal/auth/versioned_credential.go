// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
	"errors"
	"time"
)

var ErrCredentialChanged = errors.New("stored credential changed since it was read")

type VersionedCredential struct {
	Value        string
	Version      int
	Found        bool
	Disconnected bool
}

type CredentialRef struct {
	AgentName string
	AppName   string
	UserID    string
	Key       string
}

type VersionedCredentialService interface {
	GetVersioned(ctx context.Context, ref CredentialRef) (VersionedCredential, error)

	SetAt(ctx context.Context, ref CredentialRef, value string, ttl time.Duration, read VersionedCredential) (VersionedCredential, error)

	DeleteAt(ctx context.Context, ref CredentialRef, read VersionedCredential) error
}
