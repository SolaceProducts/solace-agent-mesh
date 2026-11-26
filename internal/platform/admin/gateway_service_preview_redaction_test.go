// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func newYAMLSvcForPreviewTest() *YAMLService {
	return NewYAMLService("preview-test-ns", slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestBuildGatewayConfig_KeepsRealSecretsForDeployPath(t *testing.T) {
	t.Parallel()

	yamlSvc := newYAMLSvcForPreviewTest()
	const realToken = "xoxb-runtime-real-secret-token"
	jsonValues := `{"bot_token":"` + realToken + `","app_token":"xapp-runtime-secret"}`
	gw := &store.Gateway{
		ID:     "deploy-real-secret-gw",
		Name:   "Deploy Path Slack",
		Type:   "slack",
		Values: &jsonValues,
	}

	yamlStr, err := yamlSvc.GenerateGatewayYAML(gw)
	if err != nil {
		t.Fatalf("GenerateGatewayYAML: %v", err)
	}
	if !strings.Contains(yamlStr, realToken) {
		t.Fatalf("deploy YAML lost real secret %q — reconciler push would fail:\n%s", realToken, yamlStr)
	}
	if strings.Contains(yamlStr, redactedSentinel) {
		t.Errorf("deploy YAML must not contain <REDACTED> sentinel:\n%s", yamlStr)
	}
}
