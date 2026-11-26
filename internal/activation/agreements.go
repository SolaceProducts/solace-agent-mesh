// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package activation

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/spotdraft"
)

var (
	buildSpotDraftBaseURL string
	buildEULAClickwrapID  string
)

type Agreements struct {
	BaseURL         string
	EULAClickwrapID string
}

func AgreementsFromBuild() Agreements {
	return Agreements{
		BaseURL:         buildSpotDraftBaseURL,
		EULAClickwrapID: buildEULAClickwrapID,
	}
}

func (a Agreements) Unlinked() []string {
	var missing []string
	if a.BaseURL == "" {
		missing = append(missing, "base_url")
	}
	if a.EULAClickwrapID == "" {
		missing = append(missing, "eula_clickwrap")
	}
	return missing
}

type Clickwraps interface {
	ConsentStatus(ctx context.Context, clickwrapID, userIdentifier string) (spotdraft.ConsentStatus, error)
	Clickwrap(ctx context.Context, clickwrapID string) (spotdraft.Clickwrap, error)
	Execute(ctx context.Context, clickwrap spotdraft.Clickwrap, acceptance spotdraft.Acceptance) (string, error)
}

func WithAgreements(agreements Agreements, clickwraps Clickwraps) Option {
	return func(s *Service) {
		s.agreements = agreements
		s.clickwraps = clickwraps
	}
}
