// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"log/slog"
	"os"
	"runtime"

	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

func canPrompt() bool {
	if runtime.GOOS == "windows" {
		fi, err := os.Stdin.Stat()
		return err == nil && fi.Mode()&os.ModeCharDevice != 0
	}
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func logResolvedHome(logger *slog.Logger) {
	if home, err := settings.ResolveHomeDir(); err == nil {
		logger.Info("SAM home directory", "path", home)
	}
}

func hasLLMEnv() bool {
	return os.Getenv("LLM_SERVICE_GENERAL_MODEL_NAME") != "" ||
		os.Getenv("LLM_SERVICE_PLANNING_MODEL_NAME") != ""
}

func resolveOnboarding(noOnboarding bool, logger *slog.Logger) (*settings.Settings, error) {
	if hasLLMEnv() {
		logger.Info("LLM configuration found in environment; skipping onboarding")
		return &settings.Settings{Onboarded: true}, nil
	}

	if !noOnboarding && canPrompt() {
		return settings.RunOnboarding(false)
	}

	if noOnboarding {
		logger.Info("onboarding disabled (--no-onboarding); starting with no LLM configured")
	} else {
		logger.Warn("no LLM configured and stdin is not a terminal; starting with placeholder models — set LLM_SERVICE_* (or a .env), or configure a model with `sam config apply` or the web UI before running tasks")
	}
	return &settings.Settings{Onboarded: true}, nil
}
