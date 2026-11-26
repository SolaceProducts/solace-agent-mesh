// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

const InstantLayout = "2006-01-02T15:04:05.000Z"

type Instant int64

var InstantType = reflect.TypeFor[Instant]()

func (t Instant) MarshalJSON() ([]byte, error) {
	if t == 0 {
		return []byte("null"), nil
	}
	b := append(make([]byte, 0, len(InstantLayout)+2), '"')
	b = time.UnixMilli(int64(t)).UTC().AppendFormat(b, InstantLayout)
	return append(b, '"'), nil
}

func (t *Instant) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*t = 0
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return instantTypeError()
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return instantTypeError()
	}
	*t = Instant(parsed.UnixMilli())
	return nil
}

func instantTypeError() error {
	return &json.UnmarshalTypeError{Value: "non-date-time", Type: InstantType}
}

type JSONObject json.RawMessage

var jsonObjectType = reflect.TypeFor[JSONObject]()

func (o JSONObject) MarshalJSON() ([]byte, error) {
	if len(o) == 0 {
		return []byte("null"), nil
	}
	return o, nil
}

func (o *JSONObject) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	switch {
	case bytes.Equal(trimmed, []byte("null")):
		*o = nil
	case bytes.HasPrefix(trimmed, []byte("{")):
		*o = append((*o)[:0], trimmed...)
	default:
		return &json.UnmarshalTypeError{Value: "non-object", Type: jsonObjectType}
	}
	return nil
}

type Patch[T any] struct {
	Set   bool
	Null  bool
	Value T
}

func (p *Patch[T]) UnmarshalJSON(data []byte) error {
	p.Set = true
	if string(data) == "null" {
		p.Null = true
		return nil
	}
	return decodeStrict(data, &p.Value)
}

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func InstantOf[T ~int64](t *T) Instant {
	if t == nil {
		return 0
	}
	return Instant(*t)
}

func NullIfEmpty(s *string) *string {
	if s == nil {
		return nil
	}
	return NullIfZero(*s)
}

func RequiredPatch(details map[string][]string, name string, p Patch[string]) (value *string, set bool) {
	if !p.Set {
		return nil, false
	}
	if p.Null || strings.TrimSpace(p.Value) == "" {
		details[name] = []string{"cannot be empty"}
		return nil, false
	}
	return &p.Value, true
}

func NullIfZero[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}
