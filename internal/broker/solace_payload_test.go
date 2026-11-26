// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"bytes"
	"testing"

	"solace.dev/go/messaging/pkg/solace/message/sdt"
)

type fakeInbound struct {
	str    *string
	sdtMap sdt.Map
	bytes  *[]byte
}

func (f fakeInbound) GetPayloadAsString() (string, bool) {
	if f.str != nil {
		return *f.str, true
	}
	return "", false
}

func (f fakeInbound) GetPayloadAsMap() (sdt.Map, bool) {
	if f.sdtMap != nil {
		return f.sdtMap, true
	}
	return nil, false
}

func (f fakeInbound) GetPayloadAsBytes() ([]byte, bool) {
	if f.bytes != nil {
		return *f.bytes, true
	}
	return nil, false
}

func TestPayloadFromInbound_SDTStringHasNoFraming(t *testing.T) {
	t.Parallel()

	cleanJSON := `{"CreatedDate":1783078973505,"Subject":"case created"}`

	framed := append([]byte{0x1d, 0x0c, 0x80}, []byte(cleanJSON)...)
	in := fakeInbound{str: new(cleanJSON), bytes: new(framed)}

	got := payloadFromInbound(in)

	if string(got) != cleanJSON {
		t.Fatalf("expected clean JSON payload, got %q", got)
	}
	if bytes.HasPrefix(got, []byte{0x1d}) {
		t.Fatalf("SDT framing byte leaked into payload: %q", got)
	}
}

func TestPayloadFromInbound_RawBinaryVerbatim(t *testing.T) {
	t.Parallel()

	raw := []byte{0x00, 0x01, 0x02, 0xff, 0x00, 0x7b}
	in := fakeInbound{bytes: new(raw)}

	got := payloadFromInbound(in)

	if !bytes.Equal(got, raw) {
		t.Fatalf("raw binary payload not returned verbatim: got %v want %v", got, raw)
	}
}

func TestPayloadFromInbound_SDTMapMarshalsToJSON(t *testing.T) {
	t.Parallel()

	in := fakeInbound{sdtMap: sdt.Map{"key": "value"}}

	got := payloadFromInbound(in)

	if string(got) != `{"key":"value"}` {
		t.Fatalf("SDT map not marshaled to JSON: got %q", got)
	}
}

func TestPayloadFromInbound_Empty(t *testing.T) {
	t.Parallel()

	if got := payloadFromInbound(fakeInbound{}); got != nil {
		t.Fatalf("expected nil payload for empty message, got %q", got)
	}
}
