// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	identity "github.com/SolaceProducts/solace-agent-mesh/internal/agentidentity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/credentials"
)

func identityKeySealer(t *testing.T, key string) auth.Sealer {
	t.Helper()
	km, err := identity.NewKeyManager(identity.Config{Mode: "manual", Key: key}, "agent", nil, nil)
	if err != nil {
		t.Fatalf("KeyManager: %v", err)
	}
	return credentials.NewAuthAdapter(credentials.NewSessionStore(session.NewMemoryStore(), km, nil))
}

func TestAdoptedTaskGetsTheCallersTokenBack(t *testing.T) {
	const key = "primary-key-1234567890ab"
	state := fullTaskState()
	token := state.UserProps.AuthToken
	owner := &taskCheckpointer{sealer: identityKeySealer(t, key), logger: slog.Default()}

	state.sealedAuthToken = owner.sealAuthToken(state)
	blob, err := marshalTaskState(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), token) {
		t.Fatal("the raw token reached the checkpoint blob")
	}

	adopt := func(c *taskCheckpointer) *TaskExecutionState {
		got, err := unmarshalTaskState(blob)
		if err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		c.restoreAuthToken(got, slog.Default())
		return got
	}

	got := adopt(&taskCheckpointer{sealer: identityKeySealer(t, key), logger: slog.Default()})
	if got.UserProps.AuthToken != token || got.Identity.AuthToken != token {
		t.Errorf("adopted token = %q, %q, want the caller's on both", got.UserProps.AuthToken, got.Identity.AuthToken)
	}

	for name, c := range map[string]*taskCheckpointer{
		"another identity key": {sealer: identityKeySealer(t, "different-key-abcdef12345678"), logger: slog.Default()},
		"no identity key":      {logger: slog.Default()},
	} {
		if got := adopt(c); got.UserProps.AuthToken != "" || got.Identity.AuthToken != "" {
			t.Errorf("%s: adopted with a token", name)
		}
	}
}

func TestCheckpointCarriesNoTokenWithoutOneToSeal(t *testing.T) {
	state := fullTaskState()
	if got := (&taskCheckpointer{logger: slog.Default()}).sealAuthToken(state); got != "" {
		t.Errorf("sealed %q with no identity key", got)
	}
	state.UserProps.AuthToken = ""
	c := &taskCheckpointer{sealer: identityKeySealer(t, "primary-key-1234567890ab"), logger: slog.Default()}
	if c.sealAuthToken(state) == "" {
		t.Error("the identity's token was not sealed")
	}
	state.Identity.AuthToken = ""
	if got := c.sealAuthToken(state); got != "" {
		t.Errorf("sealed %q with no token", got)
	}
}
