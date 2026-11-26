// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chromium

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

var pathNames = []string{
	"chromium",
	"chromium-browser",
	"chrome",
	"google-chrome",
	"google-chrome-stable",
}

func Find() (string, error) {
	if p := os.Getenv("SAM_CHROMIUM_PATH"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	for _, name := range pathNames {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	for _, p := range wellKnownPaths() {
		if p == "" {
			continue
		}
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("no Chromium/Chrome binary found on PATH or in the standard install locations; install chromium/chrome or set SAM_CHROMIUM_PATH to a browser binary")
}

func wellKnownPaths() []string {
	switch runtime.GOOS {
	case "darwin":
		paths := []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
		}
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			paths = append(paths,
				filepath.Join(home, "Applications", "Google Chrome.app", "Contents", "MacOS", "Google Chrome"),
				filepath.Join(home, "Applications", "Chromium.app", "Contents", "MacOS", "Chromium"),
			)
		}
		return paths
	case "windows":
		var out []string
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			base := os.Getenv(env)
			if base == "" {
				continue
			}
			out = append(out,
				filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(base, "Chromium", "Application", "chrome.exe"),
				filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
			)
		}
		return out
	default:
		return []string{
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/snap/bin/chromium",
		}
	}
}
