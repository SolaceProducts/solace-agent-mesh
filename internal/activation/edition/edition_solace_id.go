// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build solace_id

package edition

import (
	"context"
	"log/slog"
	"os"

	"github.com/SolaceProducts/solace-agent-mesh/internal/activation"
	"github.com/SolaceProducts/solace-agent-mesh/internal/freellm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/solaceid"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spotdraft"
)

func SolaceIDConfig() solaceid.Config {
	sid := solaceid.ConfigFromBuild()
	if free, linked := freellm.ConfigFromBuild(); linked {
		sid.Audience = free.Audience
	}
	return sid
}

func New(logger *slog.Logger) Gate {
	agreements := activation.AgreementsFromBuild()
	var clickwraps activation.Clickwraps
	if agreements.BaseURL != "" {
		clickwraps = spotdraft.New(agreements.BaseURL)
	}
	opts := []activation.Option{activation.WithAgreements(agreements, clickwraps)}
	free, freeModelLinked := freellm.ConfigFromBuild()
	if freeModelLinked {
		opts = append(opts, activation.WithFreeModel(free.Settings()))
	}
	if missing := agreements.Unlinked(); len(missing) > 0 {
		logger.Error("this build is missing SpotDraft values, the license agreement cannot be shown", "missing", missing)
	}
	var signIn activation.SignInClient
	if client, err := solaceid.New(SolaceIDConfig(), logger); err == nil {
		signIn = solaceIDSignIn{client: client}
	} else {
		logger.Error("this build has no Solace ID application, sign-in is unavailable", "error", err)
	}
	return activation.NewService(recordStore(logger), signIn, logger, opts...)
}

func recordStore(logger *slog.Logger) *activation.Store {
	home, err := settings.ResolveHomeDir()
	if err == nil {
		return activation.NewStore(home)
	}
	logger.Error("SAM home unresolved, activation is kept for this run only", "error", err)
	if home, err = os.MkdirTemp("", "sam-activation-"); err == nil {
		return activation.NewStore(home)
	}
	logger.Error("no directory for the activation record, SAM stays locked", "error", err)
	return activation.UnwritableStore()
}

type solaceIDSignIn struct {
	client *solaceid.Client
}

func (s solaceIDSignIn) SignIn(ctx context.Context) (activation.SignInResult, error) {
	result, err := s.client.SignIn(ctx)
	return activation.SignInResult{
		Subject: result.Account.Subject,
		Email:   result.Account.Email,
		Name:    result.Account.Name,
		Tokens: activation.FreeModelTokens{
			RefreshToken: result.Tokens.RefreshToken,
			AccessToken:  result.Tokens.AccessToken,
			ExpiresAt:    result.Tokens.Expiry,
		},
	}, err
}
