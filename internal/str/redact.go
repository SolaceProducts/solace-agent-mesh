// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"regexp"
	"strings"
	"syscall"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

var (
	envVarRe = regexp.MustCompile(
		`(?im)(?:^|[\s;])((?:[A-Z0-9_]*_API_KEY|[A-Z0-9_]*_SECRET[A-Z0-9_]*|[A-Z0-9_]*_TOKEN[A-Z0-9_]*|[A-Z0-9_]*_PASSWORD[A-Z0-9_]*|TC_[A-Z0-9_]+)\s*=\s*)\S+`,
	)

	bearerRe = regexp.MustCompile(`(?i)(Bearer\s+)\S+`)

	basicRe = regexp.MustCompile(`(?i)(Basic\s+)\S+`)

	jwtRe = regexp.MustCompile(`[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)
)

const stderrTailMaxBytes = 4 * 1024

func redactf(format string, args ...any) string {
	return redactSensitive(fmt.Sprintf(format, args...))
}

func redactSensitive(s string) string {
	s = envVarRe.ReplaceAllStringFunc(s, func(match string) string {
		loc := envVarRe.FindStringSubmatchIndex(match)
		if len(loc) < 4 {
			return match
		}
		return match[:loc[2]] + match[loc[2]:loc[3]] + "[REDACTED]"
	})
	s = bearerRe.ReplaceAllString(s, "${1}[REDACTED]")
	s = basicRe.ReplaceAllString(s, "${1}[REDACTED]")
	s = jwtRe.ReplaceAllString(s, "[REDACTED]")
	return s
}

func stderrTail(s string) string {
	if len(s) == 0 {
		return ""
	}
	if len(s) <= stderrTailMaxBytes {
		return s
	}
	dropped := len(s) - stderrTailMaxBytes
	tail := s[len(s)-stderrTailMaxBytes:]
	if idx := strings.Index(tail, "\n"); idx >= 0 {
		tail = tail[idx+1:]
	}
	return fmt.Sprintf("[... %d bytes truncated]\n", dropped) + tail
}

var pythonChainPreambles = []string{
	"During handling of the above exception, another exception occurred:",
	"The above exception was the direct cause of the following exception:",
}

func extractStackTrace(stderr string) string {
	const header = "Traceback (most recent call last):"
	if idx := strings.Index(stderr, header); idx >= 0 {
		var blocks []string
		rest := stderr[idx:]
		for {
			block := extractPythonTraceback(rest)
			if block == "" {
				break
			}
			blocks = append(blocks, block)
			after := strings.TrimLeft(rest[len(block):], "\n")
			found := false
			for _, preamble := range pythonChainPreambles {
				if strings.HasPrefix(after, preamble) {
					after = after[len(preamble):]
					nextIdx := strings.Index(after, header)
					if nextIdx >= 0 {
						rest = after[nextIdx:]
						found = true
					}
					break
				}
			}
			if !found {
				break
			}
		}
		if len(blocks) > 0 {
			return strings.Join(blocks, "\n")
		}
	}

	if idx := strings.Index(stderr, "panic:"); idx >= 0 {
		block := extractGoPanic(stderr[idx:])
		if block != "" {
			return block
		}
	}

	return ""
}

func extractPythonTraceback(s string) string {
	lines := strings.Split(s, "\n")
	end := len(lines)
	inTraceback := false
	lastContentLine := 0
	for i, line := range lines {
		if i == 0 {
			inTraceback = true
			lastContentLine = i
			continue
		}
		if !inTraceback {
			break
		}
		if line == "" {
			continue
		}
		lastContentLine = i
		if i > 1 && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			nextIdx := i + 1
			for nextIdx < end && lines[nextIdx] == "" {
				nextIdx++
			}
			if nextIdx < end && !strings.HasPrefix(lines[nextIdx], " ") && !strings.HasPrefix(lines[nextIdx], "\t") {
				lastContentLine = i
				break
			}
		}
	}
	return strings.Join(lines[:lastContentLine+1], "\n")
}

func extractGoPanic(s string) string {
	lines := strings.Split(s, "\n")
	end := 0
	sawGoroutine := false
	for i, line := range lines {
		end = i + 1
		if strings.HasPrefix(line, "goroutine ") {
			sawGoroutine = true
		}
		if sawGoroutine && line == "" {
			break
		}
	}
	return strings.Join(lines[:end], "\n")
}

func classifyRunError(toolName string, runErr error, stderrStr string) *remotetool.InvokeError {
	tail := stderrTail(stderrStr)
	trace := extractStackTrace(stderrStr)

	var execErr *exec.Error
	var pathErr *fs.PathError
	if errors.As(runErr, &execErr) || errors.As(runErr, &pathErr) {
		return &remotetool.InvokeError{
			Code:       remotetool.ErrToolExecFailed,
			Message:    redactf("tool %q could not be executed: %v", toolName, runErr),
			StderrTail: tail,
			StackTrace: trace,
		}
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				sig := status.Signal()
				msg := fmt.Sprintf("tool %q killed by signal %v", toolName, sig)
				if stderrStr != "" {
					msg = redactf("tool %q killed by signal %v: %s", toolName, sig, stderrStr)
				}
				return &remotetool.InvokeError{
					Code:       remotetool.ErrToolSignaled,
					Message:    msg,
					StderrTail: tail,
					StackTrace: trace,
				}
			}
			code := status.ExitStatus()
			msg := fmt.Sprintf("tool %q exited with code %d", toolName, code)
			if stderrStr != "" {
				msg = redactf("tool %q exited with code %d: %s", toolName, code, stderrStr)
			}
			return &remotetool.InvokeError{
				Code:       remotetool.ErrToolExitNonzero,
				Message:    msg,
				StderrTail: tail,
				StackTrace: trace,
			}
		}
		errMsg := stderrStr
		if errMsg == "" {
			errMsg = exitErr.Error()
		}
		return &remotetool.InvokeError{
			Code:       remotetool.ErrToolExitNonzero,
			Message:    redactf("tool %q failed: %s", toolName, errMsg),
			StderrTail: tail,
			StackTrace: trace,
		}
	}

	errMsg := stderrStr
	if errMsg == "" {
		errMsg = runErr.Error()
	}
	return &remotetool.InvokeError{
		Code:       remotetool.ErrToolExitNonzero,
		Message:    redactf("tool %q failed: %s", toolName, errMsg),
		StderrTail: tail,
		StackTrace: trace,
	}
}
