// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

type exampleObject[T any] struct {
	column, key string
	shape       string
	validate    func(*T) error
}

func (o exampleObject[T]) parse(text []byte) (*T, error) {
	dec := json.NewDecoder(bytes.NewReader(text))
	dec.DisallowUnknownFields()
	var out T
	err := dec.Decode(&out)
	if err == nil {
		if _, tokenErr := dec.Token(); !errors.Is(tokenErr, io.EOF) {
			err = errors.New("unexpected data after the object")
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%s must be a JSON object with %s: %v", o.column, o.shape, err)
	}
	return &out, nil
}

func (o exampleObject[T]) encode(v *T) (*string, error) {
	if v == nil {
		return nil, nil
	}
	if err := o.validate(v); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, &ValidationError{Field: o.column, Message: fmt.Sprintf("%s is not valid JSON: %v", o.key, err)}
	}
	text := string(encoded)
	return &text, nil
}

func (o exampleObject[T]) decode(raw *string) (*T, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	v, err := o.parse([]byte(*raw))
	if err == nil {
		err = o.validate(v)
	}
	if err != nil {
		return nil, fmt.Errorf("stored %s is unreadable: %w", o.column, err)
	}
	return v, nil
}

func (o exampleObject[T]) shown(raw *string) *T {
	v, _ := o.decode(raw)
	return v
}

func (o exampleObject[T]) parseCell(cell string) (*T, error) {
	cell = strings.TrimSpace(cell)
	if cell == "" || cell == "null" {
		return nil, nil
	}
	return o.parse([]byte(cell))
}

func (o exampleObject[T]) checkCell(cell string) (*T, error) {
	v, err := o.parseCell(cell)
	if err != nil || v == nil {
		return nil, err
	}
	if _, err := o.encodeForImport(v); err != nil {
		return nil, err
	}
	return v, nil
}

func (o exampleObject[T]) encodeCell(cell string) (*string, error) {
	v, err := o.parseCell(cell)
	if err != nil {
		return nil, err
	}
	return o.encodeForImport(v)
}

func (o exampleObject[T]) encodeImported(raw json.RawMessage) (*string, error) {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	v, err := o.parse(raw)
	if err != nil {
		return nil, err
	}
	return o.encodeForImport(v)
}

func (o exampleObject[T]) encodeForImport(v *T) (*string, error) {
	encoded, err := o.encode(v)
	var verr *ValidationError
	if errors.As(err, &verr) {
		return nil, errors.New(flattenDetails(verr))
	}
	return encoded, err
}

func (o exampleObject[T]) same(a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ea, errA := json.Marshal(a)
	eb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ea) == string(eb)
}

func (o exampleObject[T]) sameStored(stored *string, desired *T) bool {
	v, err := o.decode(stored)
	return err == nil && o.same(v, desired)
}

func flattenDetails(verr *ValidationError) string {
	var problems []string
	for _, field := range slices.Sorted(maps.Keys(verr.Details)) {
		for _, msg := range verr.Details[field] {
			problems = append(problems, field+": "+msg)
		}
	}
	if len(problems) == 0 {
		return verr.Message
	}
	return strings.Join(problems, "; ")
}
