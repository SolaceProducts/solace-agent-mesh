// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func volumeRoot(tc *sdk.ToolContext) (string, error) {
	if path, ok := tc.GetVolumeMountPath("/workspace"); ok {
		return path, nil
	}
	switch len(tc.VolumeMounts) {
	case 0:
		return "", fmt.Errorf(
			"no workspace volume mount; agent config must bind the tool's " +
				"workspace volume_param to a declared volume slot")
	case 1:
		return tc.VolumeMounts[0].MountPath, nil
	default:
		return "", fmt.Errorf(
			"workspace volume not resolvable: %d volumes are bound and none is mounted at /workspace; "+
				"bind the tool's workspace volume_param to a slot mounted at /workspace",
			len(tc.VolumeMounts))
	}
}

func safeJoin(root, userPath string) (string, error) {
	if userPath == "" {
		return root, nil
	}
	var joined string
	if filepath.IsAbs(userPath) {
		joined = filepath.Clean(userPath)
	} else {
		joined = filepath.Clean(filepath.Join(root, userPath))
	}

	cleanRoot := filepath.Clean(root)
	if joined != cleanRoot && !strings.HasPrefix(joined, cleanRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the workspace volume root", userPath)
	}

	if escaped, err := pathEscapesViaSymlink(cleanRoot, joined); err != nil {
		return "", fmt.Errorf("symlink check on %q: %w", userPath, err)
	} else if escaped {
		return "", fmt.Errorf("path %q resolves through a symlink that escapes the workspace volume root", userPath)
	}
	return joined, nil
}

func pathEscapesViaSymlink(root, path string) (bool, error) {
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false, fmt.Errorf("resolve root %s: %w", root, err)
	}
	canonicalRoot = filepath.Clean(canonicalRoot)

	current := path
	for current != root && current != canonicalRoot && current != "/" && current != "." {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			current = filepath.Dir(current)
			continue
		}
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return false, err
			}
			cleanResolved := filepath.Clean(resolved)
			if cleanResolved != canonicalRoot &&
				!strings.HasPrefix(cleanResolved, canonicalRoot+string(filepath.Separator)) {
				return true, nil
			}
		}
		current = filepath.Dir(current)
	}
	return false, nil
}

func refuseSymlinkedParent(root, absPath string) error {
	parent := filepath.Dir(absPath)
	info, err := os.Lstat(parent)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat parent of %s: %w", absPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	escaped, err := pathEscapesViaSymlink(filepath.Clean(root), parent)
	if err != nil {
		return fmt.Errorf("symlink check on parent of %s: %w", absPath, err)
	}
	if escaped {
		return fmt.Errorf("parent directory of %s is a symlink escaping the workspace volume root; refuse to operate through it", absPath)
	}
	return nil
}

func resolveUnderWorkspace(tc *sdk.ToolContext, userPath string) (string, error) {
	root, err := volumeRoot(tc)
	if err != nil {
		return "", err
	}
	return safeJoin(root, userPath)
}

const maxOutputBytes = 256 * 1024

func truncateForOutput(b []byte) []byte {
	return truncateForOutputTotal(b, int64(len(b)))
}

func truncateForOutputTotal(b []byte, totalBytes int64) []byte {
	if len(b) <= maxOutputBytes && totalBytes <= maxOutputBytes {
		return b
	}
	dropped := totalBytes - maxOutputBytes
	if dropped < 0 {
		dropped = 0
	}
	body := b
	if len(body) > maxOutputBytes {
		body = body[:maxOutputBytes]
	}
	marker := fmt.Sprintf("\n\n[volume_fs: output truncated; %d more bytes follow this point. Re-run with a narrower selection — line range, glob filter, or smaller path.]\n", dropped)
	return append(body, []byte(marker)...)
}
