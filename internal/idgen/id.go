// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package idgen

import "github.com/google/uuid"

func New() string {
	return uuid.Must(uuid.NewV7()).String()
}

func NewRandom() string {
	return uuid.Must(uuid.NewRandom()).String() //nolint:forbidigo // the sanctioned wrapper for a pseudonym
}

func IsCanonical(s string) bool {
	parsed, err := uuid.Parse(s)
	return err == nil && parsed.String() == s && parsed != uuid.Nil
}
