// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package volume

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ContainerMountPoint = "/host/home"

const scopeEnvOverride = "SAM_CONTAINER_HOST_MOUNT_SCOPE"

var ErrPathOutsideScope = errors.New("path is not under the configured container.host_mount_scope")

func DefaultHostMountScope() string {
	if v := os.Getenv(scopeEnvOverride); v != "" {
		if v == "none" {
			return ""
		}
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func MapHostToContainer(scope, hostPath string) (string, error) {
	if scope == "" {
		return "", fmt.Errorf("host mount scope is empty (set %s or configure container.host_mount_scope)", scopeEnvOverride)
	}
	if !filepath.IsAbs(hostPath) {
		return "", fmt.Errorf("host path must be absolute, got %q", hostPath)
	}
	cleanScope := filepath.Clean(scope)
	cleanPath := filepath.Clean(hostPath)
	rel, err := filepath.Rel(cleanScope, cleanPath)
	if err != nil {
		return "", fmt.Errorf("compute relative path of %s within %s: %w", cleanPath, cleanScope, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("%w: %s is outside %s", ErrPathOutsideScope, cleanPath, cleanScope)
	}
	if rel == "." {
		return ContainerMountPoint, nil
	}
	return ContainerMountPoint + "/" + filepath.ToSlash(rel), nil
}
