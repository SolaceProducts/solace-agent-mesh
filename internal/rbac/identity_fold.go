// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"sort"
	"strings"
)

type EmailFoldIndex struct {
	bare      map[string]string
	composite map[string]string
}

func isFoldableCompositeKey(key string) bool {
	key = strings.TrimSpace(key)
	return strings.HasPrefix(key, deprecatedGatewayPrefix) && strings.Contains(key, "@")
}

func BuildEmailFoldIndex(users map[string]UserAssignment) *EmailFoldIndex {
	idx := &EmailFoldIndex{bare: make(map[string]string), composite: make(map[string]string)}

	keys := make([]string, 0, len(users))
	for key := range users {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		switch {
		case isFoldableCompositeKey(key):
			addCanonical(idx.composite, key)
		case isFoldableEmailKey(key):
			addCanonical(idx.bare, key)
		}
	}
	return idx
}

func foldKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

func addCanonical(index map[string]string, key string) {
	folded := foldKey(key)
	if _, exists := index[folded]; !exists {
		index[folded] = key
	}
}

func (idx *EmailFoldIndex) ResolveBare(users map[string]UserAssignment, identity string) (UserAssignment, bool) {
	if assignment, ok := users[identity]; ok {
		return assignment, true
	}
	if idx == nil || !strings.Contains(identity, "@") {
		return UserAssignment{}, false
	}
	canonical, ok := idx.bare[foldKey(identity)]
	if !ok {
		return UserAssignment{}, false
	}
	assignment, ok := users[canonical]
	return assignment, ok
}

func (idx *EmailFoldIndex) ResolveComposite(users map[string]UserAssignment, compositeKey string) (UserAssignment, bool) {
	if assignment, ok := users[compositeKey]; ok {
		return assignment, true
	}
	if idx == nil || !strings.Contains(compositeKey, "@") {
		return UserAssignment{}, false
	}
	canonical, ok := idx.composite[foldKey(compositeKey)]
	if !ok {
		return UserAssignment{}, false
	}
	assignment, ok := users[canonical]
	return assignment, ok
}
