// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build desktop

package main

import (
	"fmt"
	"os"

	"github.com/SolaceProducts/solace-agent-mesh/internal/desktop"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embedded"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

func setupDesktopLauncher(provider embedded.ExtensionProvider) {
	samcli.DisableExplorerMousetrap()
	samcli.SetDesktopLauncher(func() error {
		if err := desktop.EnforcePlatformSupport(); err != nil {
			return err
		}

		opts := []desktop.RunOption{
			desktop.WithExtensionProvider(provider),
			desktop.WithDefaultConfigs(settings.DefaultConfigYAML()),
			desktop.WithAdditionalConfigs(settings.AdditionalConfigYAML()),
		}

		s, settingsPath, err := settings.LoadGlobal()
		if err != nil {
			return fmt.Errorf("loading settings: %w", err)
		}
		settingsDir := settings.SettingsDir(settingsPath)
		if s == nil {
			s = &settings.Settings{}
			if globalDir, derr := settings.DefaultGlobalDir(); derr == nil {
				settingsDir = globalDir
			}
		}
		settings.InjectIntoEnv(s, settingsDir)
		if os.Getenv("SAM_DESKTOP_SHOW_CONSOLE") != "" {
			opts = append(opts, desktop.WithShowConsole())
		}

		return desktop.Run(opts...)
	})
}
