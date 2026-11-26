// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package activation

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const fileName = "activation.yaml"

type Store struct {
	path string
}

var ErrNoStore = errors.New("activation: no directory for the activation record")

func NewStore(samHome string) *Store {
	return &Store{path: filepath.Join(samHome, fileName)}
}

func UnwritableStore() *Store {
	return &Store{}
}

func (s *Store) Load() (Record, error) {
	if s.path == "" {
		return Record{}, nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return Record{}, nil
	}
	if err != nil {
		return Record{}, fmt.Errorf("activation: read %s: %w", s.path, err)
	}
	var record Record
	if err := yaml.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("activation: parse %s: %w", s.path, err)
	}
	return record, nil
}

func (s *Store) Save(record Record) error {
	if s.path == "" {
		return ErrNoStore
	}
	data, err := yaml.Marshal(record)
	if err != nil {
		return fmt.Errorf("activation: encode record: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("activation: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".activation-*.yaml")
	if err != nil {
		return fmt.Errorf("activation: create temporary file: %w", err)
	}
	if err := writeAndClose(tmp, data); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("activation: write %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("activation: replace %s: %w", s.path, err)
	}
	return nil
}

func writeAndClose(f *os.File, data []byte) error {
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
