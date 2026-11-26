// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const desktopInstanceFile = "desktop.json"

type DesktopInstance struct {
	URL string `json:"url"`
}

func desktopInstancePath() (string, error) {
	home, err := ResolveHomeDir()
	if err != nil {
		return "", fmt.Errorf("desktop instance state: resolve home dir: %w", err)
	}
	return filepath.Join(home, desktopInstanceFile), nil
}

func WriteDesktopInstance(url string) error {
	p, err := desktopInstancePath()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("desktop instance state: create dir: %w", err)
	}
	data, err := json.MarshalIndent(DesktopInstance{URL: url}, "", "  ")
	if err != nil {
		return fmt.Errorf("desktop instance state: marshal: %w", err)
	}
	tmp := p + ".tmp"
	if err = os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("desktop instance state: write temp: %w", err)
	}
	if err = os.Rename(tmp, p); err != nil {
		return fmt.Errorf("desktop instance state: rename: %w", err)
	}
	return nil
}

func ReadDesktopInstance() (info DesktopInstance, ok bool, err error) {
	p, err := desktopInstancePath()
	if err != nil {
		return DesktopInstance{}, false, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return DesktopInstance{}, false, nil
		}
		return DesktopInstance{}, false, fmt.Errorf("desktop instance state: read %s: %w", p, err)
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return DesktopInstance{}, false, fmt.Errorf("desktop instance state: parse %s: %w", p, err)
	}
	return info, true, nil
}

func ClearDesktopInstance() error {
	p, err := desktopInstancePath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("desktop instance state: remove %s: %w", p, err)
	}
	return nil
}
