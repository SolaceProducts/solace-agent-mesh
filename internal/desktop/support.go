// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build desktop

package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

func (a *App) supportPaths() (diagnosticsPaths, error) {
	return diagnosticsPathsOrResolve(a.diagPaths)
}

func (a *App) OpenDiagnosticsFolder() error {
	paths, err := a.supportPaths()
	if err != nil {
		a.logger.Warn("could not resolve diagnostics folder", "error", err)
		return err
	}
	if err := openFolder(paths.root); err != nil {
		a.logger.Warn("could not open diagnostics folder", "error", err)
		return err
	}
	return nil
}

func (a *App) SaveDiagnosticsZip() (string, error) {
	paths, err := a.supportPaths()
	if err != nil {
		a.logger.Warn("could not resolve diagnostics folder", "error", err)
		return "", err
	}

	filename := diagnosticsZipFilename(samversion.Version, time.Now())
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	downloadsDir := filepath.Join(home, "Downloads")

	dialogPath, dialogErr := nativeSaveFileDialog(a.ctx, "Save Diagnostics Zip", filename, downloadsDir)
	if dialogErr != nil {
		a.logger.Warn("native save dialog unavailable, using Downloads folder", "error", dialogErr)
	}
	savePath, err := resolveDiagnosticsZipDestination(dialogPath, dialogErr, downloadsDir, filename)
	if err != nil {
		return "", err
	}
	if savePath == "" {
		return "", nil
	}

	if err := writeDiagnosticsZipFile(savePath, paths, time.Now()); err != nil {
		a.logger.Warn("could not write diagnostics zip", "error", err)
		return "", err
	}
	a.logger.Info("diagnostics zip saved", "path", savePath)
	return savePath, nil
}
