// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package imagemagicktool

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const coderPolicy = `  <policy domain="coder" rights="none" pattern="{URL,HTTP,HTTPS,FTP,FD,MSL,EPHEMERAL,INLINE,PRINT}" />
  <policy domain="delegate" rights="none" pattern="{URL,HTTP,HTTPS,FTP}" />
`

const (
	memoryLimit = "512MiB"
	mapLimit    = "1GiB"
	areaLimit   = "128MP"
	diskLimit   = "200MiB"

	timeLimitMargin = 5
)

const (
	configurePathEnv = "MAGICK_CONFIGURE_PATH"
	temporaryPathEnv = "MAGICK_TEMPORARY_PATH"
)

var deniedRoots = []string{"/*", "?:*"}

var globLiteral = strings.NewReplacer(`\`, "?", "[", "?", "]", "?", "*", "?", "?", "?", "{", "?", "}", "?")

const workDirPattern = "imagemagick-*-run"

func timeLimit(timeoutSeconds int) int {
	if timeoutSeconds > timeLimitMargin {
		return timeoutSeconds - timeLimitMargin
	}
	return max(timeoutSeconds, 1)
}

func resourcePolicy(timeoutSeconds int) string {
	var b strings.Builder
	for _, limit := range []struct{ name, value string }{
		{"memory", memoryLimit},
		{"map", mapLimit},
		{"area", areaLimit},
		{"disk", diskLimit},
		{"time", strconv.Itoa(timeLimit(timeoutSeconds))},
	} {
		fmt.Fprintf(&b, `  <policy domain="resource" name="%s" value="%s" />`+"\n", limit.name, limit.value)
	}
	return b.String()
}

func toolPolicy(workDirs []string, pathRules bool, timeoutSeconds int) string {
	var b strings.Builder
	b.WriteString("<policymap>\n")
	b.WriteString(coderPolicy)
	b.WriteString(resourcePolicy(timeoutSeconds))
	if pathRules {
		for _, root := range deniedRoots {
			writePathRule(&b, "none", root)
		}
		for _, pattern := range workDirPatterns(workDirs) {
			writePathRule(&b, "read|write", pattern)
		}
		writePathRule(&b, "none", "*../*")
	}
	writePathRule(&b, "none", "@*")
	b.WriteString("</policymap>\n")
	return b.String()
}

func writePathRule(b *strings.Builder, rights, pattern string) {
	fmt.Fprintf(b, `  <policy domain="path" rights="%s" pattern="%s" />`+"\n", rights, html.EscapeString(pattern))
}

func workDirPatterns(workDirs []string) []string {
	patterns := make([]string, 0, len(workDirs))
	for _, dir := range workDirs {
		patterns = append(patterns, globLiteral.Replace(dir+string(filepath.Separator))+"*")
	}
	return patterns
}

var pathRules = &pathRuleProbe{probe: probePathRules, needed: runtime.GOOS == "windows"}

type pathRuleProbe struct {
	probe  func(ctx context.Context, binary string) probeResult
	needed bool

	mu      sync.Mutex
	decided bool
	hold    bool
}

type probeResult int

const (
	probeAccepted probeResult = iota
	probeRefused
	probeInconclusive
)

func (p *pathRuleProbe) enabled(ctx context.Context, binary string) bool {
	if !p.needed {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.decided {
		return p.hold
	}
	switch p.probe(ctx, binary) {
	case probeAccepted:
		p.decided, p.hold = true, true
	case probeRefused:
		p.decided, p.hold = true, false
	case probeInconclusive:
		return true
	}
	return p.hold
}

func probePathRules(ctx context.Context, binary string) probeResult {
	logger := slog.Default().With("logger_name", "imagemagick")
	dir, err := os.MkdirTemp("", workDirPattern)
	if err != nil {
		logger.Warn("imagemagick path policy probe inconclusive", "error", err)
		return probeInconclusive
	}
	defer func() { _ = os.RemoveAll(dir) }()
	env, err := writePolicyEnv(dir, true, int(probeTimeout/time.Second))
	if err != nil {
		logger.Warn("imagemagick path policy probe inconclusive", "error", err)
		return probeInconclusive
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-size", "1x1", "xc:white", filepath.Join(dir, "probe.png"))
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	switch {
	case err == nil:
		return probeAccepted
	case strings.Contains(string(out), policyRefusal):
		logger.Warn("imagemagick path policy disabled", "reason", "imagemagick refused the tool's own work directory", "binary", binary, "output", string(out[:min(len(out), maxProbeOutput)]))
		return probeRefused
	default:
		logger.Warn("imagemagick path policy probe inconclusive", "binary", binary, "error", err)
		return probeInconclusive
	}
}

const (
	probeTimeout   = 30 * time.Second
	maxProbeOutput = 512
	policyRefusal  = "not authorized"
)

func policyEnv(ctx context.Context, binary, workDir string, timeoutSeconds int) ([]string, error) {
	return writePolicyEnv(workDir, pathRules.enabled(ctx, binary), timeoutSeconds)
}

func writePolicyEnv(workDir string, pathRules bool, timeoutSeconds int) ([]string, error) {
	workDirs := []string{workDir}
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil && resolved != workDir {
		workDirs = append(workDirs, resolved)
	}
	policyDir := filepath.Join(workDir, "policy")
	tempDir := filepath.Join(workDir, "tmp")
	for _, dir := range []string{policyDir, tempDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(filepath.Join(policyDir, "policy.xml"), []byte(toolPolicy(workDirs, pathRules, timeoutSeconds)), 0o644); err != nil {
		return nil, err
	}
	configurePath := policyDir
	if existing := os.Getenv(configurePathEnv); existing != "" {
		configurePath += string(os.PathListSeparator) + existing
	}
	return append(os.Environ(), configurePathEnv+"="+configurePath, temporaryPathEnv+"="+tempDir), nil
}
