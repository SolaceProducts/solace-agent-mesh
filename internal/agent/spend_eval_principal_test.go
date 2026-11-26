// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

const evalIssuerID = "platform-eval"

func newEvalTaskTrust(t *testing.T) (*trust.Signer, *trust.Verifier) {
	t.Helper()
	km, err := trust.NewKeyManager(evalIssuerID)
	if err != nil {
		t.Fatalf("key manager: %v", err)
	}
	registry := trust.NewRegistry()
	registry.AddOrUpdate(&trust.TrustCard{
		ComponentType: "gateway",
		ComponentID:   evalIssuerID,
		Namespace:     spendTestNamespace,
		JWKS:          km.JWKS(),
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(time.Hour).UnixMilli(),
		Version:       "1.0",
	})
	return trust.NewSigner(km, evalIssuerID, time.Hour, time.Hour), trust.NewVerifier(registry, 5*time.Second, slog.Default())
}

func TestACustomPrincipalsEvalTaskIsBookedToTheRowItsTokenSigns(t *testing.T) {
	const (
		taskID       = "eval-task-1"
		principal    = "system:qa-bot"
		principalRow = "0197a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5d"
	)
	signer, verifier := newEvalTaskTrust(t)
	evalIdentity := gwauth.NewTestSubject(gwauth.SubjectFields{
		UserID:         principal,
		PlatformUserID: principalRow,
		Scopes:         []string{"agent:*:invoke"},
		Authenticated:  true,
		AuthMethod:     "eval_system",
	})
	token, err := signer.Sign(evalIdentity.TaskSignClaims(taskID))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	agent, _, _ := newTestAgent()
	agent.svc.(*mockServices).authn = verifier

	state := runSetupTaskCapturingState(t, agent, taskID, newUserMessage("run the eval case"), a2a.UserProperties{
		ClientID:  evalIssuerID,
		UserID:    principal,
		AuthToken: token,
		Channel:   a2a.ChannelEval,
		UserUUID:  principalRow,
	})

	if id, sub := spend.SpendUser(state.UserProps, state.Identity); id != principalRow || sub != "" {
		t.Errorf("spendUser = %q/%q, want the principal's signed row %q", id, sub, principalRow)
	}
}
