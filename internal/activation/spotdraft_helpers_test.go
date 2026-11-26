// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package activation

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/spotdraft"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spotdraft/spotdrafttest"
)

const testEULAClickwrap = "eula-clickwrap"

type linkedSpotDraft struct {
	fake   *spotdrafttest.Fake
	option Option
}

var spotDrafts sync.Map

func spotDraftFor(t *testing.T, home string) (*spotdrafttest.Fake, Option) {
	t.Helper()
	if linked, ok := spotDrafts.Load(home); ok {
		return linked.(linkedSpotDraft).fake, linked.(linkedSpotDraft).option
	}
	fake := spotdrafttest.New()
	fake.AddClickwrap(testEULAClickwrap, "End User License Agreement", "LLM Credits Terms")
	server := httptest.NewServer(fake.Handler())
	t.Cleanup(server.Close)
	agreements := Agreements{BaseURL: server.URL, EULAClickwrapID: testEULAClickwrap}
	option := WithAgreements(agreements, spotdraft.New(server.URL, spotdraft.WithFirstRetryDelay(time.Millisecond)))
	spotDrafts.Store(home, linkedSpotDraft{fake: fake, option: option})
	return fake, option
}

func linkedTo(t *testing.T, home string) Option {
	t.Helper()
	_, option := spotDraftFor(t, home)
	return option
}
