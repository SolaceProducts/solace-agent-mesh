// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const callerEmail = "person@example.com"

func newCapturedLogger() (*slog.Logger, *bytes.Buffer) {
	var logs bytes.Buffer
	return slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})), &logs
}

func callerContext(t *testing.T) context.Context {
	t.Helper()
	return auth.ContextWithSubject(t.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: callerEmail, Email: callerEmail, Authenticated: true}))
}

func requireOneRefusalLine(t *testing.T, logs *bytes.Buffer, message, changedField string, forbidden ...string) map[string]any {
	t.Helper()
	var found []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) == nil && entry["msg"] == message {
			found = append(found, entry)
		}
	}
	if len(found) != 1 {
		t.Fatalf("lines with message %q = %d, want 1; logs: %s", message, len(found), logs.String())
	}
	if found[0]["level"] != "WARN" || found[0]["changedField"] != changedField || found[0]["userFP"] != strutil.PIIFingerprint(callerEmail) {
		t.Errorf("line = %v, want WARN naming %q and the caller's fingerprint", found[0], changedField)
	}
	if _, named := found[0]["userID"]; named {
		t.Errorf("line = %v, want no raw user id", found[0])
	}
	for _, secret := range append([]string{callerEmail}, forbidden...) {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("the logs contain %q", secret)
		}
	}
	return found[0]
}

func TestConnectorConnectionTestRefusalIsLoggedOnceWithoutTheDestinationOrSecrets(t *testing.T) {
	logger, logs := newCapturedLogger()
	svc := NewConnectorService(store.NewConnectorRepo(testOpenDB(t)), nil, "test-ns", logger)
	id := createStoredEventMeshConnector(t, svc)
	logs.Reset()
	recordBrokerDials(t)

	_, err := svc.TestEventMeshConnection(callerContext(t), TestConnectorConnectionRequest{ConnectorID: id, Values: map[string]any{"broker_url": "tcps://evil.example.net:55443"}})

	requireBindingRefusal(t, err)
	entry := requireOneRefusalLine(t, logs, msgProbeRefusedStoredCredentials, "broker_url", "evil.example.net", "broker.example.com", storedConnectorBrokerPassword, storedConnectorBrokerUsername)
	if entry["connectorID"] != id {
		t.Errorf("connectorID = %v, want %s", entry["connectorID"], id)
	}
}

func TestEntrypointConnectionTestRefusalIsLoggedOnceWithoutTheDestinationOrSecrets(t *testing.T) {
	logger, logs := newCapturedLogger()
	db := testOpenDB(t)
	svc := NewGatewayService(store.NewGatewayRepo(db), store.NewGatewayDeploymentRepo(db), nil, nil, nil, logger)
	id := createEventMeshGatewayForTest(t, svc, storedEventMeshEntrypointValues())
	logs.Reset()
	recordBrokerDials(t)

	_, err := svc.TestConnection(callerContext(t), TestConnectionRequest{GatewayID: id, Values: map[string]any{"broker_url": "tcp://evil.example.net:55555"}})

	requireBindingRefusal(t, err)
	entry := requireOneRefusalLine(t, logs, msgProbeRefusedStoredCredentials, "broker_url", "evil.example.net", "stored.example.com", storedEntrypointPassword, "stored-user")
	if entry["gatewayID"] != id || entry["gatewayType"] != "event_mesh" {
		t.Errorf("refusal line = %v, want the entrypoint id and type", entry)
	}
}

func TestRefusalWithoutAnIdentityNamesNoOne(t *testing.T) {
	logger, logs := newCapturedLogger()

	logProbeRefusal(t.Context(), logger, "broker_url", "connectorID", "c-1")

	if strings.Contains(logs.String(), "userID") || strings.Contains(logs.String(), "userFP") {
		t.Errorf("a refusal with no identity still names a user: %s", logs.String())
	}
}

func TestRefusalNamesAResolvedPlatformUserByIDAndNeverByEmail(t *testing.T) {
	logger, logs := newCapturedLogger()
	ctx := auth.ContextWithSubject(t.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: callerEmail, Email: callerEmail, PlatformUserID: "0190a5f0-0000-7000-8000-0000000000aa"}))

	logProbeRefusal(ctx, logger, "broker_url")

	if !strings.Contains(logs.String(), `"userID":"0190a5f0-0000-7000-8000-0000000000aa"`) || strings.Contains(logs.String(), callerEmail) || strings.Contains(logs.String(), "userFP") {
		t.Errorf("refusal line = %s, want the platform user id and no email or fingerprint", logs.String())
	}
}
