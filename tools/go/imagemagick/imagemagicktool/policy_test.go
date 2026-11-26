// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package imagemagicktool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func runMagick(t *testing.T, env []string, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "magick", args...)
	cmd.Env = env
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestToolPolicyIsEnforcedByImageMagick(t *testing.T) {
	requireMagick(t)
	workDir := t.TempDir()
	env, err := writePolicyEnv(workDir, true, defaultTimeout)
	if err != nil {
		t.Fatalf("writePolicyEnv: %v", err)
	}

	for _, source := range []string{"inline:data:image/gif;base64,R0lGODlhAQABAAAAACw=", "https://127.0.0.1:1/x.png"} {
		out, runErr := runMagick(t, env, workDir, source, filepath.Join(workDir, "out.png"))
		if runErr == nil || !strings.Contains(out, "security policy") {
			t.Errorf("magick %s: err = %v, output = %q, want a security policy denial", source, runErr, out)
		}
	}
}

func TestToolPolicyConfinesFileAccessToTheWorkDirectory(t *testing.T) {
	requireMagick(t)
	if got := probePathRules(t.Context(), "magick"); got != probeAccepted {
		t.Fatalf("probePathRules = %v, want ImageMagick to accept the path rules on its own work directory", got)
	}
	workDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.png")
	if out, err := runMagick(t, os.Environ(), workDir, "-size", "4x4", "xc:blue", outside); err != nil {
		t.Fatalf("create the outside image: %v: %s", err, out)
	}
	relative, err := filepath.Rel(workDir, outside)
	if err != nil {
		t.Fatal(err)
	}
	env, err := writePolicyEnv(workDir, true, defaultTimeout)
	if err != nil {
		t.Fatalf("writePolicyEnv: %v", err)
	}

	for name, href := range map[string]string{"absolute": outside, "relative": relative} {
		t.Run("svg href "+name, func(t *testing.T) {
			svg := filepath.Join(workDir, name+".svg")
			body := `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="4" height="4"><image width="4" height="4" xlink:href="` + href + `"/></svg>`
			if err := os.WriteFile(svg, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			out, _ := runMagick(t, env, workDir, svg, filepath.Join(workDir, name+".png"))
			if !strings.Contains(out, "security policy") {
				t.Errorf("output = %q, want the href outside the work directory denied", out)
			}
		})
	}
	t.Run("direct read", func(t *testing.T) {
		out, runErr := runMagick(t, env, workDir, outside, filepath.Join(workDir, "copy.png"))
		if runErr == nil || !strings.Contains(out, "security policy") {
			t.Errorf("err = %v, output = %q, want the read outside the work directory denied", runErr, out)
		}
	})
	t.Run("write inside", func(t *testing.T) {
		if out, err := runMagick(t, env, workDir, "-size", "4x4", "xc:red", filepath.Join(workDir, "inside.png")); err != nil {
			t.Errorf("write inside the work directory failed: %v: %s", err, out)
		}
	})
}

func TestToolPolicyMatchesAWorkDirectoryWithGlobCharacters(t *testing.T) {
	requireMagick(t)
	parent := t.TempDir()
	workDir := filepath.Join(parent, "w[1]{a,b}*?")
	neighbour := filepath.Join(parent, "w1ab")
	for _, dir := range []string{workDir, neighbour} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env, err := writePolicyEnv(workDir, true, defaultTimeout)
	if err != nil {
		t.Fatalf("writePolicyEnv: %v", err)
	}
	if out, err := runMagick(t, env, workDir, "-size", "4x4", "xc:red", filepath.Join(workDir, "inside.png")); err != nil {
		t.Errorf("write inside the work directory failed: %v: %s", err, out)
	}
	if out, err := runMagick(t, env, workDir, "-size", "4x4", "xc:red", filepath.Join(neighbour, "x.png")); err == nil || !strings.Contains(out, "security policy") {
		t.Errorf("err = %v, output = %q, want the write to a directory the raw glob would match denied", err, out)
	}
}

func TestToolPolicyWithoutPathRulesKeepsTheCoderRules(t *testing.T) {
	policy := toolPolicy([]string{"/w"}, false, defaultTimeout)
	if strings.Contains(policy, `pattern="/*"`) || !strings.Contains(policy, "INLINE") || !strings.Contains(policy, `pattern="@*"`) {
		t.Errorf("policy = %q, want the coder and @ rules without path confinement", policy)
	}
}

func TestWorkDirPatterns(t *testing.T) {
	got := workDirPatterns([]string{`/tmp/a[1]{b}*?\c`})
	want := "/tmp/a?1??b????c" + globLiteral.Replace(string(filepath.Separator)) + "*"
	if len(got) != 1 || got[0] != want {
		t.Errorf("workDirPatterns = %q, want [%q]", got, want)
	}
}

func TestToolPolicyKeepsSVGConversionWorking(t *testing.T) {
	requireMagick(t)
	workDir := t.TempDir()
	svg := filepath.Join(workDir, "in.svg")
	if err := os.WriteFile(svg, []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"><rect width="4" height="4" fill="red"/></svg>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runMagick(t, os.Environ(), workDir, svg, filepath.Join(workDir, "plain.png")); err != nil {
		t.Skipf("this ImageMagick cannot read SVG at all: %s", out)
	}
	env, err := writePolicyEnv(workDir, true, defaultTimeout)
	if err != nil {
		t.Fatalf("writePolicyEnv: %v", err)
	}
	if out, err := runMagick(t, env, workDir, svg, filepath.Join(workDir, "out.png")); err != nil {
		t.Errorf("SVG conversion failed under the tool policy: %v: %s", err, out)
	}
}

func TestProbePathRulesTellsARefusalFromAnotherFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ImageMagick is a POSIX shell script")
	}
	cases := map[string]probeResult{
		"exit 0": probeAccepted,
		"echo 'not authorized `/x/probe.png'; exit 1": probeRefused,
		"echo 'no decode delegate'; exit 1":           probeInconclusive,
	}
	for script, want := range cases {
		binary := filepath.Join(t.TempDir(), "magick")
		if err := os.WriteFile(binary, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := probePathRules(t.Context(), binary); got != want {
			t.Errorf("probe of %q = %v, want %v", script, got, want)
		}
	}
}

func TestPathRuleProbeKeepsOnlyADefiniteAnswer(t *testing.T) {
	var results []probeResult
	calls := 0
	probe := &pathRuleProbe{needed: true, probe: func(context.Context, string) probeResult {
		calls++
		result := results[0]
		results = results[1:]
		return result
	}}

	results = []probeResult{probeInconclusive, probeRefused}
	if !probe.enabled(t.Context(), "magick") {
		t.Error("an inconclusive probe turned the path rules off")
	}
	if probe.enabled(t.Context(), "magick") {
		t.Error("a refusal left the path rules on")
	}
	if probe.enabled(t.Context(), "magick") || calls != 2 {
		t.Errorf("after a refusal: calls = %d, want the refusal kept without probing again", calls)
	}

	skipped := &pathRuleProbe{probe: func(context.Context, string) probeResult {
		t.Error("probed where the path rules are verified")
		return probeRefused
	}}
	if !skipped.enabled(t.Context(), "magick") {
		t.Error("path rules off where no probe is needed")
	}
}

func TestToolPolicyCapsResourcesBelowTheToolTimeout(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout int
		want    []string
	}{
		{"default timeout", 60, []string{`name="memory" value="512MiB"`, `name="map" value="1GiB"`, `name="area" value="128MP"`, `name="disk" value="200MiB"`, `name="time" value="55"`}},
		{"long timeout", 120, []string{`name="time" value="115"`}},
		{"timeout at the margin", 5, []string{`name="time" value="5"`}},
		{"tiny timeout", 2, []string{`name="time" value="2"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := toolPolicy([]string{"/w"}, true, tc.timeout)
			for _, want := range tc.want {
				if !strings.Contains(policy, `<policy domain="resource" `+want) {
					t.Errorf("policy = %q, want a resource rule %s", policy, want)
				}
			}
		})
	}
}

func TestToolPolicyResourceLimitsAreAcceptedByImageMagick(t *testing.T) {
	requireMagick(t)
	workDir := t.TempDir()
	env, err := writePolicyEnv(workDir, true, defaultTimeout)
	if err != nil {
		t.Fatalf("writePolicyEnv: %v", err)
	}
	listing, err := runMagick(t, env, workDir, "-list", "resource")
	if err != nil {
		t.Fatalf("magick -list resource: %v: %s", err, listing)
	}
	for _, want := range []string{`Memory:\s*512MiB`, `Map:\s*1GiB`, `Area:\s*128MP`, `Disk:\s*200MiB`, `Time:\s*55`} {
		if !regexp.MustCompile(want).MatchString(listing) {
			t.Errorf("effective limits = %q, want a line matching %s", listing, want)
		}
	}
	if small, smallErr := runMagick(t, env, workDir, "-size", "64x64", "xc:white", filepath.Join(workDir, "ok.png")); smallErr != nil {
		t.Errorf("magick under the resource limits: %v: %s", smallErr, small)
	}
	out, err := runMagick(t, env, workDir, "-size", "20000x20000", "xc:white", filepath.Join(workDir, "big.png"))
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || out == "" {
		t.Errorf("err = %v, output = %q, want exit code 1 with stderr for an image over the area limit", err, out)
	}
}
