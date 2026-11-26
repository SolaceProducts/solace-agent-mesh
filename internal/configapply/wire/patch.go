// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"bytes"
	"encoding/json"
)

type Patch[T any] struct {
	Set   bool
	Null  bool
	Value T
}

func (p Patch[T]) IsZero() bool { return !p.Set }

func (p Patch[T]) PatchState() (set, null bool) { return p.Set, p.Null }

func (p Patch[T]) MarshalJSON() ([]byte, error) {
	if p.Null {
		return []byte("null"), nil
	}
	return json.Marshal(p.Value)
}

func (p *Patch[T]) UnmarshalJSON(data []byte) error {
	p.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		p.Null = true
		return nil
	}
	return json.Unmarshal(data, &p.Value)
}
