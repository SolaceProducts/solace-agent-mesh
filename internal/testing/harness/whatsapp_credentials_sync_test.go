// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
)

func TestWhatsAppTestCredentialsStayInSync(t *testing.T) {
	sourcetree.RequireInternal(t)
	root := sourcetree.Root(t)
	ts := readFile(t, filepath.Join(root, "test", "e2e", "helpers", "whatsapp.ts"))
	yaml := readFile(t, filepath.Join(root, "configs-e2e-harness", "whatsapp-gateway.yaml"))

	for _, tt := range []struct {
		name    string
		value   string
		tsKey   string
		yamlKey string
	}{
		{name: "access token has no tsKey because the helper never sends outbound", value: whatsappTestAccessToken, yamlKey: "access_token"},
		{name: "phone number id", value: whatsappTestPhoneNumberID, tsKey: "phoneNumberId", yamlKey: "phone_number_id"},
		{name: "app secret", value: whatsappTestAppSecret, tsKey: "appSecret", yamlKey: "app_secret"},
		{name: "verify token", value: whatsappTestVerifyToken, tsKey: "verifyToken", yamlKey: "verify_token"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.tsKey != "" && !strings.Contains(ts, tt.tsKey+`: "`+tt.value+`"`) {
				t.Errorf("test/e2e/helpers/whatsapp.ts is out of sync: expected %s: %q", tt.tsKey, tt.value)
			}
			if !strings.Contains(yaml, tt.yamlKey+`: "`+tt.value+`"`) {
				t.Errorf("configs-e2e-harness/whatsapp-gateway.yaml is out of sync: expected %s: %q", tt.yamlKey, tt.value)
			}
		})
	}
}

func TestWhatsAppIdentityGatewayCredentialsStayInSync(t *testing.T) {
	sourcetree.RequireInternal(t)
	root := sourcetree.Root(t)
	ts := readFile(t, filepath.Join(root, "test", "e2e", "helpers", "whatsapp.ts"))
	yaml := readFile(t, filepath.Join(root, "configs-e2e-harness", "whatsapp-identity", "whatsapp-identity-gateway.yaml"))

	const identityPhoneNumberID = "test-identity-phone-number-id"
	if !strings.Contains(ts, `phoneNumberId: "`+identityPhoneNumberID+`"`) {
		t.Errorf("test/e2e/helpers/whatsapp.ts is out of sync: expected WHATSAPP_IDENTITY_TEST.phoneNumberId %q", identityPhoneNumberID)
	}
	for yamlKey, value := range map[string]string{
		"access_token":    whatsappTestAccessToken,
		"phone_number_id": identityPhoneNumberID,
		"app_secret":      whatsappTestAppSecret,
		"verify_token":    whatsappTestVerifyToken,
	} {
		if !strings.Contains(yaml, yamlKey+`: "`+value+`"`) {
			t.Errorf("configs-e2e-harness/whatsapp-identity/whatsapp-identity-gateway.yaml is out of sync: expected %s: %q", yamlKey, value)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
