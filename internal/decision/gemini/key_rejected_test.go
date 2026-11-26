// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gemini

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

func TestStatusErrorKeyRejected(t *testing.T) {
	cases := map[string]struct {
		err  *StatusError
		want bool
	}{
		"400 API_KEY_INVALID": {&StatusError{StatusCode: http.StatusBadRequest, KeyInvalid: true}, true},
		"401":                 {&StatusError{StatusCode: http.StatusUnauthorized}, true},
		"other 400":           {&StatusError{StatusCode: http.StatusBadRequest}, false},
		"403":                 {&StatusError{StatusCode: http.StatusForbidden}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			wrapped := &decision.Error{Kind: tc.err.kind(), Err: fmt.Errorf("google_ai_studio: %w", tc.err)}
			if got := decision.KeyRejected(wrapped); got != tc.want {
				t.Errorf("KeyRejected = %v, want %v", got, tc.want)
			}
		})
	}
}
