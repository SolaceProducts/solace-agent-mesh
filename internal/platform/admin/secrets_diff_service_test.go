// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGatewayService_GetValuesCleartext_ReturnsUnredacted(t *testing.T) {
	db := testOpenDB(t)
	svc := NewGatewayService(store.NewGatewayRepo(db), store.NewGatewayDeploymentRepo(db), nil, nil, nil, silentLogger())

	created, err := svc.Create(context.Background(), CreateGatewayRequest{
		Name:        "diff-gw",
		Description: "Gateway for cleartext-accessor testing",
		Type:        "slack",
		Values:      map[string]any{"bot_token": "xoxb-stored", "app_token": "xapp-stored", "channel": "#ops"},
	}, "user1")
	if err != nil {
		t.Fatalf("create gateway: %v", err)
	}

	gwType, values, err := svc.GetValuesCleartext(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetValuesCleartext: %v", err)
	}
	if gwType != "slack" {
		t.Errorf("gwType = %q; want slack", gwType)
	}
	if values["bot_token"] != "xoxb-stored" {
		t.Errorf("bot_token = %v; want xoxb-stored (cleartext)", values["bot_token"])
	}
	if values["app_token"] != "xapp-stored" {
		t.Errorf("app_token = %v; want xapp-stored (cleartext)", values["app_token"])
	}
	if values["channel"] != "#ops" {
		t.Errorf("channel = %v; want #ops", values["channel"])
	}
}

func TestGatewayService_GetValuesCleartext_UnknownID(t *testing.T) {
	db := testOpenDB(t)
	svc := NewGatewayService(store.NewGatewayRepo(db), store.NewGatewayDeploymentRepo(db), nil, nil, nil, silentLogger())

	_, _, err := svc.GetValuesCleartext(context.Background(), "00000000-0000-0000-0000-000000000000")
	var nfe *NotFoundError
	if !errors.As(err, &nfe) {
		t.Errorf("err = %v; want NotFoundError", err)
	}
}

func TestConnectorService_GetValuesCleartext_ReturnsUnredacted(t *testing.T) {
	db := testOpenDB(t)
	svc := NewConnectorService(store.NewConnectorRepo(db), nil, "test-ns", silentLogger())

	created, err := svc.Create(context.Background(), CreateConnectorRequest{
		Name:        "diff-conn",
		Description: "Connector for cleartext-accessor testing",
		Type:        "messaging",
		Subtype:     "slack",
		Values:      map[string]any{"slack_bot_token": "xoxb-stored", "slack_channel": "#ops"},
	}, "user1")
	if err != nil {
		t.Fatalf("create connector: %v", err)
	}

	connType, subtype, values, err := svc.GetValuesCleartext(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetValuesCleartext: %v", err)
	}
	if connType != "messaging" || subtype != "slack" {
		t.Errorf("type/subtype = %q/%q; want messaging/slack", connType, subtype)
	}
	if values["slack_bot_token"] != "xoxb-stored" {
		t.Errorf("slack_bot_token = %v; want xoxb-stored (cleartext)", values["slack_bot_token"])
	}
	if values["slack_channel"] != "#ops" {
		t.Errorf("slack_channel = %v; want #ops", values["slack_channel"])
	}
}

func TestConnectorService_GetValuesCleartext_UnknownID(t *testing.T) {
	db := testOpenDB(t)
	svc := NewConnectorService(store.NewConnectorRepo(db), nil, "test-ns", silentLogger())

	_, _, _, err := svc.GetValuesCleartext(context.Background(), "00000000-0000-0000-0000-000000000000")
	var nfe *NotFoundError
	if !errors.As(err, &nfe) {
		t.Errorf("err = %v; want NotFoundError", err)
	}
}

func TestModelConfigService_GetAuthConfigCleartext_ReturnsUnredacted(t *testing.T) {
	db := testOpenDB(t)
	svc := NewModelConfigService(store.NewModelConfigRepo(db), silentLogger())
	svc.SetFeatureRegistry(decisionNodesOn())

	apiBase := "https://api.anthropic.com"
	created, err := svc.Create(context.Background(), &CreateModelConfigRequest{
		Alias:     "diff-model",
		Provider:  "anthropic",
		ModelName: "claude-opus-4-7",
		APIBase:   apiBase,
		AuthType:  "apikey",
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": "sk-ant-stored",
		},
	}, "user1")
	if err != nil {
		t.Fatalf("create model: %v", err)
	}

	authType, authConfig, err := svc.GetAuthConfigCleartext(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetAuthConfigCleartext: %v", err)
	}
	if authType != "apikey" {
		t.Errorf("authType = %q; want apikey", authType)
	}
	if authConfig["api_key"] != "sk-ant-stored" {
		t.Errorf("api_key = %v; want sk-ant-stored (cleartext)", authConfig["api_key"])
	}
}

func TestModelConfigService_GetAuthConfigCleartext_UnknownID(t *testing.T) {
	db := testOpenDB(t)
	svc := NewModelConfigService(store.NewModelConfigRepo(db), silentLogger())
	svc.SetFeatureRegistry(decisionNodesOn())

	_, _, err := svc.GetAuthConfigCleartext(context.Background(), "00000000-0000-0000-0000-000000000000")
	var nfe *NotFoundError
	if !errors.As(err, &nfe) {
		t.Errorf("err = %v; want NotFoundError", err)
	}
}
