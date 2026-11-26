// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapigen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

const (
	FullSpecFile   = "sam-openapi-full.yaml"
	PublicSpecFile = "sam-openapi.yaml"
)

const yamlIndent = 2

func Marshal(doc *openapi3.T) ([]byte, error) {
	raw, err := doc.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal spec json: %w", err)
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("decode spec json: %w", err)
	}
	generic = wholeNumbersAsIntegers(generic)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)
	if err := enc.Encode(generic); err != nil {
		return nil, fmt.Errorf("encode spec yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close yaml encoder: %w", err)
	}
	return buf.Bytes(), nil
}

func WriteSpecs(dir string, specs *Specs) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	names := []string{FullSpecFile, PublicSpecFile}
	docs := map[string]*openapi3.T{FullSpecFile: specs.Full, PublicSpecFile: specs.Public}
	staged := map[string]string{}
	cleanup := func() {
		for _, tmp := range staged {
			_ = os.Remove(tmp)
		}
	}
	for _, name := range names {
		data, err := Marshal(docs[name])
		if err != nil {
			cleanup()
			return fmt.Errorf("render %s: %w", name, err)
		}
		tmp, err := stage(dir, name, data)
		if err != nil {
			cleanup()
			return err
		}
		staged[name] = tmp
	}
	return publish(dir, names, staged, cleanup)
}

func publish(dir string, names []string, staged map[string]string, cleanup func()) error {
	previous := map[string][]byte{}
	for _, name := range names {
		old, err := os.ReadFile(filepath.Join(dir, name))
		switch {
		case err == nil:
			previous[name] = old
		case !errors.Is(err, fs.ErrNotExist):
			cleanup()
			return fmt.Errorf("read the existing %s: %w", name, err)
		}
	}
	var replaced []string
	for _, name := range names {
		if err := os.Rename(staged[name], filepath.Join(dir, name)); err != nil {
			cleanup()
			return errors.Join(fmt.Errorf("publish %s: %w", name, err), restore(dir, replaced, previous))
		}
		delete(staged, name)
		replaced = append(replaced, name)
	}
	return nil
}

func restore(dir string, replaced []string, previous map[string][]byte) error {
	var errs []error
	for _, name := range replaced {
		path := filepath.Join(dir, name)
		old, existed := previous[name]
		if !existed {
			errs = append(errs, os.Remove(path))
			continue
		}
		tmp, err := stage(dir, name, old)
		if err == nil {
			err = os.Rename(tmp, path)
			if err != nil {
				_ = os.Remove(tmp)
			}
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func stage(dir, name string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, name+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("stage %s: %w", name, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("write %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("close %s: %w", name, err)
	}
	return f.Name(), nil
}

func Validate(doc *openapi3.T) error {
	data, err := Marshal(doc)
	if err != nil {
		return err
	}
	loaded, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		return fmt.Errorf("reload rendered spec: %w", err)
	}
	if err := loaded.Validate(context.Background()); err != nil {
		return fmt.Errorf("validate rendered spec: %w", err)
	}
	return nil
}

func wholeNumbersAsIntegers(node any) any {
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			v[key] = wholeNumbersAsIntegers(child)
		}
	case []any:
		for i, child := range v {
			v[i] = wholeNumbersAsIntegers(child)
		}
	case float64:
		if v == math.Trunc(v) && math.Abs(v) < maxExactInteger {
			return int64(v)
		}
	}
	return node
}

const maxExactInteger = 1 << 53
