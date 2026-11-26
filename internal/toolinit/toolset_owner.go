// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

var toolsetMemberNames = sync.OnceValue(func() map[string]struct{} {
	members := map[string]struct{}{}
	for group := range schema.ValidBuiltinGroups {
		for _, name := range LookupBuiltinGroup(group, Options{}) {
			members[name] = struct{}{}
		}
	}
	return members
})

func BuiltinToolHasToolset(tool string) bool {
	_, ok := toolsetMemberNames()[tool]
	return ok
}
