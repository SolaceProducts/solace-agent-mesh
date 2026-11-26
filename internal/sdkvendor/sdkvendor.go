// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sdkvendor

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed embed/gosdk
var goSDKFS embed.FS

const (
	goSDKRoot = "embed/gosdk"

	vendorSubdir = "_sdk"

	goSDKVendorName = "samtoolsdk"
)

const errMissingToolDirFmt = "tool dir does not exist: %s"

func VendorDirName() string { return vendorSubdir }

func GoSDKDirName() string { return goSDKVendorName }

func EnsureGoSDK(toolDir string) (injected bool, err error) {
	if !dirExists(toolDir) {
		return false, fmt.Errorf(errMissingToolDirFmt, toolDir)
	}
	dest := filepath.Join(toolDir, vendorSubdir, goSDKVendorName)
	if dirHasFiles(dest) {
		return false, nil
	}
	if err := writeGoSDK(dest); err != nil {
		return false, err
	}
	return true, nil
}

func ForceGoSDK(toolDir string) error {
	if !dirExists(toolDir) {
		return fmt.Errorf(errMissingToolDirFmt, toolDir)
	}
	dest := filepath.Join(toolDir, vendorSubdir, goSDKVendorName)
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("clear vendor dir %s: %w", dest, err)
	}
	return writeGoSDK(dest)
}

func writeGoSDK(dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("create vendor dir %s: %w", dest, err)
	}
	return fs.WalkDir(goSDKFS, goSDKRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == goSDKRoot {
			return nil
		}
		rel, err := filepath.Rel(goSDKRoot, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dest, rel), 0o755)
		}
		data, err := goSDKFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}
		outRel := rel
		switch rel {
		case "gomod.txt":
			outRel = "go.mod"
		default:
			if base, ok := strings.CutSuffix(outRel, ".gotxt"); ok {
				outRel = base + ".go"
			}
		}
		out := filepath.Join(dest, outRel)
		rel2, err := filepath.Rel(dest, out)
		if err != nil || strings.HasPrefix(rel2, "..") {
			return fmt.Errorf("embedded path escapes destination: %s", outRel)
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o644)
	})
}

func dirHasFiles(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
