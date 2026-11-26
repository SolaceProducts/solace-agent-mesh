// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

var protectedModelAliases = map[string]struct{}{
	"general":  {},
	"planning": {},
}

func IsProtectedModelAlias(alias string) bool {
	_, protected := protectedModelAliases[alias]
	return protected
}
