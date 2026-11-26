// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"fmt"
	"testing"
)

func TestClassifyDispatchError_DelegatesToTheSharedTaxonomy(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"a cancellation is caller-domain", context.Canceled, "canceled"},
		{"a deadline is infrastructure", context.DeadlineExceeded, "timeout"},
		{"an unrecognised failure stays loud", fmt.Errorf("something else"), "internal"},
		{"no error classifies as none", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyDispatchError(tc.err); got != tc.want {
				t.Errorf("classifyDispatchError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
