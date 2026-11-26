// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import "fmt"

type StateSigner struct {
	s *Signer
}

func NewStateSigner(s *Signer) *StateSigner { return &StateSigner{s: s} }

func (ss *StateSigner) Mint(claims StateClaims) (string, error) {
	if ss == nil || ss.s == nil {
		return "", fmt.Errorf("state signer: nil signer")
	}
	return ss.s.SignTyped(StateJWTType, claims)
}
