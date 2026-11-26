// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

func TestResolveExecTimeout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		def        int
		max        int
		entry      *ManifestEntry
		params     *remotetool.InvokeParams
		wantSecs   int
		wantSource string
	}{
		{
			name:       "nothing declared falls back to the sandbox default",
			def:        300,
			entry:      &ManifestEntry{},
			params:     &remotetool.InvokeParams{},
			wantSecs:   300,
			wantSource: timeoutSourceDefault,
		},
		{
			name:       "manifest outranks the default",
			def:        300,
			entry:      &ManifestEntry{TimeoutSeconds: 90},
			params:     &remotetool.InvokeParams{},
			wantSecs:   90,
			wantSource: timeoutSourceManifest,
		},
		{
			name:       "explicit caller value outranks the manifest",
			def:        300,
			entry:      &ManifestEntry{TimeoutSeconds: 90},
			params:     &remotetool.InvokeParams{TimeoutSeconds: 30},
			wantSecs:   30,
			wantSource: timeoutSourceCaller,
		},
		{
			name:       "caller zero defers to the manifest",
			def:        300,
			entry:      &ManifestEntry{TimeoutSeconds: 90},
			params:     &remotetool.InvokeParams{TimeoutSeconds: 0},
			wantSecs:   90,
			wantSource: timeoutSourceManifest,
		},
		{
			name:       "caller value applies with no manifest entry",
			def:        300,
			entry:      &ManifestEntry{},
			params:     &remotetool.InvokeParams{TimeoutSeconds: 45},
			wantSecs:   45,
			wantSource: timeoutSourceCaller,
		},
		{
			name:       "nil entry and params are tolerated",
			def:        300,
			entry:      nil,
			params:     nil,
			wantSecs:   300,
			wantSource: timeoutSourceDefault,
		},
		{
			name:       "a manifest above the ceiling is capped at the default max",
			def:        300,
			entry:      &ManifestEntry{TimeoutSeconds: 86400},
			params:     &remotetool.InvokeParams{},
			wantSecs:   DefaultMaxToolTimeoutSeconds,
			wantSource: timeoutSourceMax,
		},
		{
			name:       "a caller above the configured ceiling is capped",
			def:        300,
			max:        600,
			entry:      &ManifestEntry{TimeoutSeconds: 90},
			params:     &remotetool.InvokeParams{TimeoutSeconds: 1200},
			wantSecs:   600,
			wantSource: timeoutSourceMax,
		},
		{
			name:       "a manifest at the ceiling is not reported as capped",
			def:        300,
			entry:      &ManifestEntry{TimeoutSeconds: DefaultMaxToolTimeoutSeconds},
			params:     &remotetool.InvokeParams{},
			wantSecs:   DefaultMaxToolTimeoutSeconds,
			wantSource: timeoutSourceManifest,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			secs, source := resolveExecTimeout(tc.def, tc.max, tc.entry, tc.params)
			if secs != tc.wantSecs {
				t.Errorf("seconds = %d, want %d", secs, tc.wantSecs)
			}
			if source != tc.wantSource {
				t.Errorf("source = %q, want %q", source, tc.wantSource)
			}
		})
	}
}

func TestAdvertisedExecTimeout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		cfg   SandboxConfig
		entry *ManifestEntry
		want  int
	}{
		{name: "no declared timeout advertises the built-in default", entry: &ManifestEntry{}, want: DefaultRemoteToolTimeoutSeconds},
		{name: "no declared timeout advertises the configured default", cfg: SandboxConfig{DefaultTimeoutSeconds: 120}, entry: &ManifestEntry{}, want: 120},
		{name: "a declared timeout is advertised", entry: &ManifestEntry{TimeoutSeconds: 900}, want: 900},
		{name: "a declared timeout above the ceiling advertises the ceiling", cfg: SandboxConfig{MaxTimeoutSeconds: 600}, entry: &ManifestEntry{TimeoutSeconds: 86400}, want: 600},
		{name: "the default ceiling applies when none is configured", entry: &ManifestEntry{TimeoutSeconds: 86400}, want: DefaultMaxToolTimeoutSeconds},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := advertisedExecTimeout(tc.cfg, tc.entry); got != tc.want {
				t.Errorf("advertisedExecTimeout = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDefaultTimeoutExceedsSDKLLMCallCap(t *testing.T) {
	t.Parallel()
	const sdkLLMCallCapSeconds = 240
	if DefaultRemoteToolTimeoutSeconds <= sdkLLMCallCapSeconds {
		t.Errorf("DefaultRemoteToolTimeoutSeconds = %d, must exceed the samtoolsdk LLM call cap of %ds "+
			"so a stalled in-tool LLM call surfaces as a catchable tool error, not a sandbox kill",
			DefaultRemoteToolTimeoutSeconds, sdkLLMCallCapSeconds)
	}
}

func TestSandboxHonorsManifestWhenCallerSendsZero(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	script := writeScript(t, dir, "slow.sh", `sleep 30`)

	runner := NewSandboxRunner(SandboxConfig{
		Mode:                    "direct",
		WorkBaseDir:             t.TempDir(),
		DefaultTimeoutSeconds:   60,
		MaxConcurrentExecutions: 4,
	}, slog.Default())

	entry := minimalEntry(script)
	entry.TimeoutSeconds = 2

	params := minimalParams()

	_, invokeErr := runner.Execute(t.Context(), params, entry, nil, func(string) {})
	if invokeErr == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if invokeErr.Code != remotetool.ErrToolTimeout {
		t.Fatalf("code = %q, want %q", invokeErr.Code, remotetool.ErrToolTimeout)
	}
	if !strings.Contains(invokeErr.Message, "timed out after 2s") {
		t.Errorf("message = %q, want the manifest's 2s to be honored", invokeErr.Message)
	}
}

func TestExplicitCallerTimeoutOverridesManifest(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	script := writeScript(t, dir, "slow.sh", `sleep 30`)

	runner := NewSandboxRunner(SandboxConfig{
		Mode:                    "direct",
		WorkBaseDir:             t.TempDir(),
		DefaultTimeoutSeconds:   60,
		MaxConcurrentExecutions: 4,
	}, slog.Default())

	entry := minimalEntry(script)
	entry.TimeoutSeconds = 3

	params := minimalParams()
	params.TimeoutSeconds = 1

	start := time.Now()
	_, invokeErr := runner.Execute(t.Context(), params, entry, nil, func(string) {})
	elapsed := time.Since(start)

	if invokeErr == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if !strings.Contains(invokeErr.Message, "timed out after 1s") {
		t.Errorf("message = %q, want the caller's explicit 1s to win", invokeErr.Message)
	}

	const execGrace = 5 * time.Second
	if elapsed < 1*time.Second+execGrace-500*time.Millisecond || elapsed > 1*time.Second+execGrace+2*time.Second {
		t.Errorf("elapsed = %v, want ~%v (1s budget + %v exec grace)", elapsed, 1*time.Second+execGrace, execGrace)
	}
}
