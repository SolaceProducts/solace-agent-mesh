// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package activation

import (
	"context"
	"errors"
	"time"
)

type State string

const (
	StateSignedOut   State = "signed_out"
	StateEULAPending State = "eula_pending"
	StateActive      State = "active"
)

var (
	ErrSignInUnavailable      = errors.New("activation: this build has no Solace ID application configured")
	ErrAlreadySignedIn        = errors.New("activation: already signed in")
	ErrNotSignedIn            = errors.New("activation: not signed in")
	ErrFreeModelUnavailable   = errors.New("activation: this build has no free model")
	ErrFreeModelNotOffered    = errors.New("activation: the free model is chosen only before activation")
	ErrFreeModelGrantNotSaved = errors.New("activation: the free model's sign-in could not be saved")
	ErrNoRefreshToken         = errors.New("activation: the sign-in returned no refresh token for the free model")
	ErrFreeModelNotChosen     = errors.New("activation: the free model is not set up on this install")
	ErrDifferentAccount       = errors.New("activation: the sign-in was by another Solace ID account")
	ErrRecordNotSaved         = errors.New("activation: the record could not be saved")
)

type SignInResult struct {
	Subject string
	Email   string
	Name    string
	Tokens  FreeModelTokens
}

type FreeModelTokens struct {
	RefreshToken string
	AccessToken  string
	ExpiresAt    time.Time
}

type FreeModel interface {
	SaveGrant(ctx context.Context, result SignInResult) error
	Refresh(ctx context.Context) error
	DeleteGrant(ctx context.Context) error
}

type FreeModelSettings struct {
	GrantID           string
	Provider          string
	APIBase           string
	GeneralModelName  string
	PlanningModelName string
}

type SignInClient interface {
	SignIn(ctx context.Context) (SignInResult, error)
}

type SignInFailure interface {
	error
	SignInFailureReason() string
}

type Status struct {
	State              State
	SigningIn          bool
	SignInError        error
	Account            *Account
	Agreements         Agreements
	FreeModelAvailable bool
	FreeModel          *FreeModelSettings
	FreeModelChosen    bool
}
