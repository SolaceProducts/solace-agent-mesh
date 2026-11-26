// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"errors"
	"testing"
)

func TestNetworkDevBroker_BindExisting_Unsupported(t *testing.T) {
	t.Parallel()
	c := NewNetworkDevBroker("127.0.0.1:0", "unused", "unused", testLogger())
	_, err := c.BindExisting("preprov/q/x")
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("BindExisting err = %v, want errors.ErrUnsupported", err)
	}
}

func TestNetworkDevBroker_ConsumeExisting_Unsupported(t *testing.T) {
	t.Parallel()
	c := NewNetworkDevBroker("127.0.0.1:0", "unused", "unused", testLogger())
	_, err := c.ConsumeExisting("preprov/q/x")
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("ConsumeExisting err = %v, want errors.ErrUnsupported", err)
	}
}
