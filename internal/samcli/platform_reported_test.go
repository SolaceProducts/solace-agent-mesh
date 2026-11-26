// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
)

const platformTokenEnv = "SAM_PLATFORM_TOKEN"

func writePlatformManifest(t *testing.T, url string) string {
	t.Helper()
	mf := filepath.Join(t.TempDir(), "manifest.yaml")
	body := "kind: manifest\nname: test\ntarget:\n  name: prod\n  url: " + url +
		"\n  auth:\n    type: bearer_token\n    env_var: " + platformTokenEnv + "\n"
	if err := os.WriteFile(mf, []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return mf
}

func bearerTarget(url string) target.Target {
	return target.Target{
		URL:  url,
		Auth: target.AuthSpec{Type: target.AuthBearerToken, EnvVar: platformTokenEnv},
	}
}

func TestRecordAddressedPlatformCarriesTheResolvedCredentials(t *testing.T) {
	isolateGatewayRecording(t)
	t.Setenv(platformTokenEnv, "platform-token")

	recordAddressedPlatform(t.Context(), bearerTarget("https://sam.example.com/"))

	got := recordedGateway()
	if got.BaseURL != "https://sam.example.com" {
		t.Errorf("BaseURL = %q, want the resolved platform origin", got.BaseURL)
	}
	if got.Token != "platform-token" {
		t.Error("the token the command resolved was not carried through")
	}
}

func TestRecordAddressedPlatformIgnoresAnEmptyURL(t *testing.T) {
	isolateGatewayRecording(t)
	recordAddressedPlatform(t.Context(), bearerTarget(""))
	if got := recordedGateway(); got.Addressed() {
		t.Errorf("recordedGateway() = %+v, want the zero Gateway", got)
	}
}

func TestRecordAddressedPlatformRecordsAnAnonymousDeployment(t *testing.T) {
	isolateGatewayRecording(t)
	rerouteHome(t)
	t.Setenv(platformTokenEnv, "")

	recordAddressedPlatform(t.Context(), target.Target{
		URL:  "http://127.0.0.1:8800",
		Auth: target.AuthSpec{Type: target.AuthOAuth},
	})

	got := recordedGateway()
	if !got.Addressed() {
		t.Fatal("an anonymous platform still owns the run")
	}
	if got.Token != "" {
		t.Errorf("Token = %q, want empty for an anonymous deployment", got.Token)
	}
}

func TestPlatformCommandsRecordTheDeploymentTheyAddressed(t *testing.T) {
	for _, tc := range []struct {
		name string
		args func(url string) []string
	}{
		{"config apply", func(url string) []string {
			return []string{"config", "apply", "--manifest", writePlatformManifest(t, url), "--no-interactive"}
		}},
		{"config plan", func(url string) []string {
			return []string{"config", "plan", "--manifest", writePlatformManifest(t, url), "--no-interactive"}
		}},
		{"config pull", func(url string) []string {
			return []string{"config", "pull", "--output", t.TempDir(), "--url", url}
		}},
		{"toolset package", func(url string) []string {
			return []string{"toolset", "package", "nonexistent-toolset", "--url", url}
		}},
		{"skill package", func(url string) []string {
			return []string{"skill", "package", "nonexistent-skill", "--url", url}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateGatewayRecording(t)
			rerouteHome(t)
			t.Setenv(platformTokenEnv, "platform-token")

			const url = "http://127.0.0.1:1"
			cmd := newRootCmd()
			cmd.SetOut(new(bytes.Buffer))
			cmd.SetErr(new(bytes.Buffer))
			cmd.SetArgs(tc.args(url))
			_ = cmd.Execute()

			got := recordedGateway()
			if !got.Addressed() {
				t.Fatalf("%s recorded no gateway, so the run reports under this host's install", tc.name)
			}
			if got.BaseURL != url {
				t.Errorf("BaseURL = %q, want %q", got.BaseURL, url)
			}
		})
	}
}

func TestRecordAddressedPlatformIgnoresAnUnresolvableTokenCache(t *testing.T) {
	isolateGatewayRecording(t)
	t.Setenv(platformTokenEnv, "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	recordAddressedPlatform(t.Context(), target.Target{
		URL:  "https://sam.example.com",
		Auth: target.AuthSpec{Type: target.AuthOAuth},
	})

	if got := recordedGateway(); got.Addressed() {
		t.Errorf("recordedGateway() = %+v, want the zero Gateway when the token cache can't be located", got)
	}
}

func TestRecordAddressedPlatformIgnoresAResolveFailureThatIsNotUnauthenticated(t *testing.T) {
	isolateGatewayRecording(t)
	t.Setenv(platformTokenEnv, "")

	recordAddressedPlatform(t.Context(), bearerTarget("https://sam.example.com"))

	if got := recordedGateway(); got.Addressed() {
		t.Errorf("recordedGateway() = %+v, want the zero Gateway for a non-auth resolve failure", got)
	}
}

func TestOfflinePackageRecordsNoDeployment(t *testing.T) {
	isolateGatewayRecording(t)
	rerouteHome(t)

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"toolset", "package", "nonexistent-toolset"})
	_ = cmd.Execute()

	if got := recordedGateway(); got.Addressed() {
		t.Errorf("recordedGateway() = %+v, want the zero Gateway for an offline package", got)
	}
}
