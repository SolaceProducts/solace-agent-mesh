// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package securitytest

import (
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
)

var rawHandlerAllowlist = map[string]string{
	"internal/testing/harness/harness.go":       "canary capture point — production ReplaceAttr would mask the marker under test",
	"internal/testing/harness/proxy_harness.go": "canary capture point — same rationale as harness.go",
	"test/lifecycle/harness/mockllm.go":         "lifecycle-test harness output, not a production sink",
	"test/lifecycle/harness/broker.go":          "lifecycle-test harness output, not a production sink",
	"internal/logging/setup.go":                 "defines NewHandler — the raw constructors here are the implementation",
}

var rawHandlerPattern = regexp.MustCompile(`slog\.New(Text|JSON)Handler\(`)

func TestNoRawSlogHandlersOutsideAllowlist(t *testing.T) {
	roots := []string{"cmd", "common", "internal", "pkg", "third_party", "tools"}
	if sourcetree.IsInternal(t) {
		roots = append(roots, "test")
	}
	repoRoot := filepath.Join("..", "..")

	var offenders []string
	for _, root := range roots {
		err := filepath.WalkDir(filepath.Join(repoRoot, root), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == ".claude" || d.Name() == "node_modules" || d.Name() == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if !rawHandlerPattern.Match(src) {
				return nil
			}
			rel := filepath.ToSlash(strings.TrimPrefix(filepath.ToSlash(path), filepath.ToSlash(repoRoot)+"/"))
			if _, ok := rawHandlerAllowlist[rel]; !ok {
				offenders = append(offenders, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}

	if len(offenders) > 0 {
		t.Errorf("raw slog handler constructed outside logging.NewHandler in:\n  %s\n\nThese loggers run with no ReplaceAttr chain, so MaskSecretAttr never sees their records. Use logging.NewStderrLogger for a bootstrap logger, or logging.NewHandler(w, opts, format) for any other sink. If a sink genuinely must stay raw, add it to rawHandlerAllowlist with the reason.",
			strings.Join(offenders, "\n  "))
	}
}

func TestRawHandlerAllowlistHasNoStaleEntries(t *testing.T) {
	internal := sourcetree.IsInternal(t)
	for rel, reason := range rawHandlerAllowlist {
		path := filepath.Join("..", "..", rel)
		src, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) && !internal {
			continue
		}
		if err != nil {
			t.Errorf("allowlisted %q (%s) does not exist — remove the entry", rel, reason)
			continue
		}
		if !rawHandlerPattern.Match(src) {
			t.Errorf("allowlisted %q (%s) no longer constructs a raw handler — remove the entry", rel, reason)
		}
	}
}

func TestBootstrapLoggerMasksSecretAttrs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(logging.NewHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}, "text"))

	logger.Error("startup failed",
		"api_key", "sk-live-must-not-appear",
		"client_secret", "shhh-must-not-appear",
		"agentName", "orchestrator")

	out := buf.String()
	for _, secret := range []string{"sk-live-must-not-appear", "shhh-must-not-appear"} {
		if strings.Contains(out, secret) {
			t.Errorf("secret-keyed value reached the log verbatim: %s", out)
		}
	}
	if !strings.Contains(out, "orchestrator") {
		t.Errorf("non-secret attribute was lost: %s", out)
	}
}

func TestRawHandlerDoesNotMask(t *testing.T) {
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})).
		Error("startup failed", "api_key", "sk-live-would-appear")

	if !strings.Contains(buf.String(), "sk-live-would-appear") {
		t.Skip("a raw slog handler no longer emits secret-keyed values verbatim; masking is no longer provided by logging.NewHandler alone")
	}
}
