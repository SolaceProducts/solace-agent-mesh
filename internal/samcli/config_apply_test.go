// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

func writeDesktopManifest(t *testing.T) string {
	t.Helper()
	mf := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(mf, []byte("kind: manifest\nname: test\ntarget:\n  name: desktop\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return mf
}

func TestLoadConfigContext_DesktopTargetFromManifest(t *testing.T) {
	rerouteHome(t)
	if err := settings.WriteDesktopInstance("http://127.0.0.1:54321"); err != nil {
		t.Fatalf("WriteDesktopInstance: %v", err)
	}

	_, _, tg, err := loadConfigContext(&cobra.Command{}, &configFlags{manifestPath: writeDesktopManifest(t)})
	if err != nil {
		t.Fatalf("loadConfigContext: %v", err)
	}
	if tg.URL != "http://127.0.0.1:54321" {
		t.Errorf("tg.URL = %q; want the desktop state-file origin", tg.URL)
	}
}

func TestLoadConfigContext_DesktopTargetNotRunning(t *testing.T) {
	rerouteHome(t)

	_, _, _, err := loadConfigContext(&cobra.Command{}, &configFlags{manifestPath: writeDesktopManifest(t)})
	if err == nil {
		t.Fatal("expected an error when no desktop state file exists")
	}
	if !strings.Contains(err.Error(), "no running SAM desktop app") {
		t.Errorf("error = %q; want it to mention no running desktop", err)
	}
}

func TestConfigCommandStructure(t *testing.T) {
	cmd := newConfigCmd()

	expected := []string{"apply", "plan", "pull"}
	for _, name := range expected {
		found := false
		for _, sub := range cmd.Commands() {
			if sub.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("config subcommand %q not found", name)
		}
	}
}

func TestConfigPullRequiresOutput(t *testing.T) {
	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{"config", "pull"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected non-nil error from `sam config pull` without --output")
	}
	if !strings.Contains(err.Error(), "output") {
		t.Errorf("error should mention --output requirement, got: %v", err)
	}
}

func TestConfigPullRequiresTarget(t *testing.T) {
	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{"config", "pull", "--output", t.TempDir()})

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected non-nil error when --target is missing")
	}
}

func TestConfigPull_LossyPullPrintsNoSummary(t *testing.T) {
	page := func(rows string) string {
		return `{"data":[` + rows + `],"meta":{"pagination":{"pageNumber":1,"pageSize":100,"count":1,"totalPages":1}}}`
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/agents", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(page(`{"id":"a1","name":".."}`)))
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(page("")))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	out := filepath.Join(t.TempDir(), "repo")
	cmd := newRootCmd()
	stdout := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"config", "pull", "--output", out, "--url", srv.URL, "--only", "agent"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("err = %v; want the lossy-pull error", err)
	}
	if strings.Contains(stdout.String(), "Wrote") {
		t.Errorf("stdout = %q; want no summary", stdout.String())
	}
	if _, statErr := os.Stat(out); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("output dir exists (stat err %v); want nothing written", statErr)
	}
}

func TestConfigCommandRegisteredOnRoot(t *testing.T) {
	root := newRootCmd()
	found := false
	for _, sub := range root.Commands() {
		if sub.Name() == "config" {
			found = true
			break
		}
	}
	if !found {
		t.Error("`sam config` not registered on root command")
	}
}

func TestConfigPlan_NoManifestFlagAndNoDefaultFile(t *testing.T) {
	t.Chdir(t.TempDir())

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"config", "plan"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --manifest is missing and no default file exists")
	}
	msg := err.Error()
	if !strings.Contains(msg, "manifest.yaml") || !strings.Contains(msg, "manifest.yml") {
		t.Errorf("error should name both default candidates, got: %v", err)
	}
}

func TestConfigPlan_AutoDiscoversManifestYaml(t *testing.T) {
	dir := t.TempDir()
	mf := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(mf, []byte("kind: agent\nname: x\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	t.Chdir(dir)

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	stderr := new(bytes.Buffer)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"config", "plan"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected manifest-parse error (kind != manifest)")
	}
	if strings.Contains(err.Error(), "--manifest is required") {
		t.Fatalf("auto-discovery should have picked up ./manifest.yaml, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "using auto-detected manifest") {
		t.Errorf("expected stderr to announce auto-detection, got: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "manifest.yaml") {
		t.Errorf("stderr announcement should name the chosen file, got: %q", stderr.String())
	}
}

func TestConfigPlan_AutoDiscoversManifestYml(t *testing.T) {
	dir := t.TempDir()
	mf := filepath.Join(dir, "manifest.yml")
	if err := os.WriteFile(mf, []byte("kind: agent\nname: x\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	t.Chdir(dir)

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	stderr := new(bytes.Buffer)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"config", "plan"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected manifest-parse error (kind != manifest)")
	}
	if !strings.Contains(stderr.String(), "manifest.yml") {
		t.Errorf("stderr announcement should name manifest.yml, got: %q", stderr.String())
	}
}

func TestConfigPlan_BadManifestFile(t *testing.T) {
	dir := t.TempDir()
	mf := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(mf, []byte("kind: agent\nname: x\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"config", "plan", "--manifest", mf})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when manifest kind != manifest")
	}
}

func TestDiscoverDefaultManifest(t *testing.T) {
	t.Run("prefers yaml over yml", func(t *testing.T) {
		dir := t.TempDir()
		yamlPath := filepath.Join(dir, "manifest.yaml")
		ymlPath := filepath.Join(dir, "manifest.yml")
		if err := os.WriteFile(yamlPath, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ymlPath, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := discoverDefaultManifest(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != yamlPath {
			t.Errorf("got %q, want %q", got, yamlPath)
		}
	})

	t.Run("falls back to yml", func(t *testing.T) {
		dir := t.TempDir()
		ymlPath := filepath.Join(dir, "manifest.yml")
		if err := os.WriteFile(ymlPath, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := discoverDefaultManifest(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != ymlPath {
			t.Errorf("got %q, want %q", got, ymlPath)
		}
	})

	t.Run("returns error when nothing matches", func(t *testing.T) {
		_, err := discoverDefaultManifest(t.TempDir())
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "manifest.yaml") || !strings.Contains(err.Error(), "manifest.yml") {
			t.Errorf("error should name both candidates, got: %v", err)
		}
	})

	t.Run("ignores a directory named manifest.yaml", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "manifest.yaml"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := discoverDefaultManifest(dir); err == nil {
			t.Fatal("expected error: directory must not satisfy file discovery")
		}
	})
}

func TestConfigApplyPrune_NonInteractiveRefuses(t *testing.T) {
	withInteractive(t, false)
	cmd := newConfigApplyCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs([]string{"--prune"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a refusal error from --prune without --force in non-interactive mode")
	}
	if !strings.Contains(err.Error(), "--prune") || !strings.Contains(err.Error(), "--force") {
		t.Errorf("error = %v; want a 'refusing to --prune … --force' message", err)
	}
}

func TestConfigApplyPrune_InteractiveAbort(t *testing.T) {
	withInteractive(t, true)
	cmd := newConfigApplyCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetIn(strings.NewReader("n\n"))
	cmd.SetArgs([]string{"--prune"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("declined prune should fail with a not-confirmed error, got %v", err)
	}
}

func TestConfigApplyPrune_NoInteractiveRefusesOnTTY(t *testing.T) {
	withInteractive(t, true)
	cmd := newConfigApplyCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetIn(strings.NewReader("y\n"))
	cmd.SetArgs([]string{"--prune", "--no-interactive"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("--no-interactive --prune without --force should refuse, got %v", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestConfirmPrompt(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"Y\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"no\n", false},
		{"\n", false},
		{"", false},
		{"maybe\n", false},
	}
	for _, c := range cases {
		cmd := &cobra.Command{}
		var errOut bytes.Buffer
		cmd.SetErr(&errOut)
		cmd.SetIn(strings.NewReader(c.in))

		got, err := confirmPrompt(cmd, "Proceed?")
		if err != nil {
			t.Fatalf("confirmPrompt(%q) returned error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("confirmPrompt(%q) = %v; want %v", c.in, got, c.want)
		}
		if !strings.Contains(errOut.String(), "Proceed?") {
			t.Errorf("confirmPrompt(%q) should write the question to stderr; got %q", c.in, errOut.String())
		}
	}
}

func TestConfirmPrompt_ReadError(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetIn(errReader{})

	got, err := confirmPrompt(cmd, "Proceed?")
	if err == nil {
		t.Fatal("expected an error from a failing reader")
	}
	if got {
		t.Errorf("confirmPrompt on read error = %v; want false", got)
	}
}

func TestConfigDotEnv_ExistingEnvWinsOverCheckoutDotEnvAndFillsUnset(t *testing.T) {
	dir := t.TempDir()
	dotenv := "SAM_TEST_DOTENV_CI_TOKEN=from-dotenv\nSAM_TEST_DOTENV_ONLY_IN_FILE=from-dotenv\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(dotenv), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("SAM_TEST_DOTENV_CI_TOKEN", "from-ci")
	t.Setenv("SAM_TEST_DOTENV_ONLY_IN_FILE", "")
	if err := os.Unsetenv("SAM_TEST_DOTENV_ONLY_IN_FILE"); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"config", "plan"})
	_ = cmd.Execute()

	if got := os.Getenv("SAM_TEST_DOTENV_CI_TOKEN"); got != "from-ci" {
		t.Errorf("SAM_TEST_DOTENV_CI_TOKEN = %q, want the exported %q", got, "from-ci")
	}
	if got := os.Getenv("SAM_TEST_DOTENV_ONLY_IN_FILE"); got != "from-dotenv" {
		t.Errorf("SAM_TEST_DOTENV_ONLY_IN_FILE = %q, want %q from .env", got, "from-dotenv")
	}
}

func TestConfigEnvFile_OverridesExistingEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SAM_TEST_ENVFILE_TOKEN=from-dotenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(dir, "override.env")
	if err := os.WriteFile(override, []byte("SAM_TEST_ENVFILE_TOKEN=from-env-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("SAM_TEST_ENVFILE_TOKEN", "from-ci")

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"config", "plan", "--env-file", override})
	_ = cmd.Execute()

	if got := os.Getenv("SAM_TEST_ENVFILE_TOKEN"); got != "from-env-file" {
		t.Errorf("SAM_TEST_ENVFILE_TOKEN = %q, want %q from --env-file", got, "from-env-file")
	}
}

func TestFormatOptions_NoColorEnvStripsColorEvenOnATerminal(t *testing.T) {
	tty, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tty.Close() })
	cmd := &cobra.Command{}
	cmd.SetOut(tty)

	t.Setenv("NO_COLOR", "")
	if formatOptions(cmd, &configFlags{}, false).NoColor {
		t.Fatal("a terminal without NO_COLOR should keep color")
	}
	t.Setenv("NO_COLOR", "1")
	if !formatOptions(cmd, &configFlags{}, false).NoColor {
		t.Error("NO_COLOR=1 should disable color")
	}
}

func TestEnvFlagSet(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false}, {"0", false}, {"false", false}, {"FALSE", false}, {" 0 ", false},
		{"1", true}, {"true", true}, {"yes", true},
	} {
		t.Setenv("SAM_TEST_FLAG", tc.value)
		if got := envFlagSet("SAM_TEST_FLAG"); got != tc.want {
			t.Errorf("envFlagSet(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}
