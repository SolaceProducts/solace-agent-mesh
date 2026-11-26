// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"context"
	"slices"
	"strconv"
	"strings"
)

func UniqueArtifactNames(prefix string, keys []string) []string {
	seen := make(map[string]struct{})
	var names []string
	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		rel := key[len(prefix):]
		name, after, found := strings.Cut(rel, "/")
		if name == "" || !found || after == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func ArtifactVersionsByName(prefix string, keys []string) map[string][]int {
	versions := make(map[string][]int)
	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		rel := key[len(prefix):]
		name, verStr, found := strings.Cut(rel, "/")
		if name == "" || !found || verStr == "" {
			continue
		}
		v, err := strconv.Atoi(verStr)
		if err != nil {
			continue
		}
		versions[name] = append(versions[name], v)
	}
	for name := range versions {
		slices.Sort(versions[name])
	}
	return versions
}

type StrictKeyLister interface {
	ListKeysStrict(ctx context.Context, scope KeyScope) ([]string, error)
}

const maxDecoratorDepth = 16

func ListKeysStrict(ctx context.Context, svc Service, scope KeyScope) ([]string, error) {
	current := svc
	for range maxDecoratorDepth {
		if lister, ok := current.(StrictKeyLister); ok {
			return lister.ListKeysStrict(ctx, scope)
		}
		unwrapper, ok := current.(Unwrapper)
		if !ok || unwrapper.Inner() == nil {
			break
		}
		current = unwrapper.Inner()
	}
	return svc.ListKeys(ctx, scope)
}
