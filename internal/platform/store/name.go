// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import "strings"

func normalizeName(name string) string {
	return NormalizeName(name)
}

func NormalizeName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, " ", ""))
}

func BudgetNameLower(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
