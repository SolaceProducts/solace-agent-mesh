// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import "testing"

type fakeBinder struct {
	*DevBroker
	bound bool
}

func (f *fakeBinder) Bound(Queue) bool { return f.bound }

func TestBound(t *testing.T) {
	q := &SimpleQueue{QName: "q"}
	if !Bound(NewDevBroker(), q) {
		t.Fatal("a broker that binds at create must read as bound")
	}
	lazy := &fakeBinder{DevBroker: NewDevBroker()}
	if Bound(lazy, q) {
		t.Fatal("a lazy broker read as bound before its bind")
	}
	lazy.bound = true
	if !Bound(lazy, q) {
		t.Fatal("a lazy broker did not read as bound after its bind")
	}
}
