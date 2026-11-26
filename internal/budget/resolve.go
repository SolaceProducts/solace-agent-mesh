// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budget

type Source string

const (
	SourceAssigned Source = "assigned"
	SourceDefault  Source = "default"
)

func Resolve[B any](assigned, tierDefault *B) (*B, Source) {
	switch {
	case assigned != nil:
		return assigned, SourceAssigned
	case tierDefault != nil:
		return tierDefault, SourceDefault
	default:
		return nil, ""
	}
}
