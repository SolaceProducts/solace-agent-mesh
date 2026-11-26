// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package volume

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const sidecarFilename = "volume.json"

type sidecar struct {
	ID                    string `json:"id"`
	Linked                bool   `json:"linked"`
	ExternalPath          string `json:"external_path,omitempty"`
	ExternalPathContainer string `json:"external_path_in_container,omitempty"`
}

func writeLinkedSidecar(volumeDir, id, externalPath, externalPathContainer string) error {
	sc := sidecar{
		ID:                    id,
		Linked:                true,
		ExternalPath:          externalPath,
		ExternalPathContainer: externalPathContainer,
	}
	data, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal volume sidecar: %w", err)
	}
	path := filepath.Join(volumeDir, sidecarFilename)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write volume sidecar %s: %w", path, err)
	}
	return nil
}

func readSidecar(volumeDir string) (*sidecar, error) {
	path := filepath.Join(volumeDir, sidecarFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read volume sidecar %s: %w", path, err)
	}
	var sc sidecar
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("parse volume sidecar %s: %w", path, err)
	}
	return &sc, nil
}
