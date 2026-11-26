// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const pythonProbeTimeout = 5 * time.Second

func strPythonVersion() string {
	_, version := resolveSTRPython()
	return version
}

func probePythonVersion(interp string) string {
	ctx, cancel := context.WithTimeout(context.Background(), pythonProbeTimeout)
	defer cancel()
	return probePythonVersionContext(ctx, interp)
}

func probePythonVersionContext(ctx context.Context, interp string) string {
	cmd := exec.CommandContext(ctx, interp, "--version")
	hideConsoleWindow(cmd)
	cmd.WaitDelay = time.Second
	out, err := childCombinedOutput(cmd)
	if err != nil {
		return ""
	}
	return parsePythonMajorMinor(strings.TrimSpace(string(out)))
}

func parsePythonMajorMinor(s string) string {
	after, ok := strings.CutPrefix(s, "Python ")
	if !ok {
		return ""
	}
	parts := strings.SplitN(after, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	major := strings.TrimSpace(parts[0])
	minor := strings.TrimSpace(parts[1])
	if major == "" || minor == "" {
		return ""
	}
	for i, c := range minor {
		if c < '0' || c > '9' {
			minor = minor[:i]
			break
		}
	}
	if major == "" || minor == "" {
		return ""
	}
	return major + "." + minor
}
