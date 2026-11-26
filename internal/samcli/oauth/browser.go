// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oauth

import (
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"sync/atomic"
)

var browserOverride atomic.Pointer[func(string) error]

func OverrideBrowser(fn func(string) error) {
	if fn == nil {
		browserOverride.Store(nil)
		return
	}
	browserOverride.Store(&fn)
}

func OpenBrowser(url string) error {
	if v := browserOverride.Load(); v != nil {
		return (*v)(url)
	}
	cmd, err := browserCommand(url)
	if err != nil {
		return err
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch browser: %w", err)
	}
	_ = cmd.Process.Release()
	return nil
}

func browserCommand(url string) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url), nil
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url), nil
	default:
		path, err := exec.LookPath("xdg-open")
		if err != nil {
			return nil, fmt.Errorf("no browser launcher available (xdg-open not found in PATH); open %s manually", url)
		}
		return exec.Command(path, url), nil
	}
}
