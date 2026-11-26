// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import (
	"fmt"
	"os/exec"
	"runtime"
)

var startFileManager = func(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func openFolder(dir string) error {
	name, args := openFolderCommand(runtime.GOOS, dir)
	if err := startFileManager(name, args...); err != nil {
		return fmt.Errorf("open folder in file manager: %w", err)
	}
	return nil
}

func openFolderCommand(goos, dir string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{dir}
	case "windows":
		return "explorer", []string{dir}
	default:
		return "xdg-open", []string{dir}
	}
}
