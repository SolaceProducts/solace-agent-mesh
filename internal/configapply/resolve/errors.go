// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package resolve

import (
	"fmt"
	"sort"
	"strings"

	configloader "github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

type UnresolvedVarsError struct {
	Files       map[string][]string
	VaultErrors map[string][]configloader.VaultRefError
}

func (e *UnresolvedVarsError) Error() string {
	if e == nil || (len(e.Files) == 0 && len(e.VaultErrors) == 0) {
		return "no unresolved variables"
	}

	var b strings.Builder
	varTotal := 0
	for _, vs := range e.Files {
		varTotal += len(vs)
	}
	vaultTotal := 0
	for _, vs := range e.VaultErrors {
		vaultTotal += len(vs)
	}

	if varTotal > 0 {
		noun := "reference"
		if varTotal != 1 {
			noun = "references"
		}
		fmt.Fprintf(&b, "config error: %d unresolved environment variable %s:", varTotal, noun)
		paths := make([]string, 0, len(e.Files))
		for p := range e.Files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			for _, v := range e.Files[p] {
				fmt.Fprintf(&b, "\n  %s\t(%s)", v, p)
			}
		}
		b.WriteString("\nset the variables, or use ${VAR, default} / ${VAR:-default} to opt in to unset.")
	}

	if vaultTotal > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		noun := "reference"
		if vaultTotal != 1 {
			noun = "references"
		}
		fmt.Fprintf(&b, "config error: %d unresolved vault %s:", vaultTotal, noun)
		paths := make([]string, 0, len(e.VaultErrors))
		for p := range e.VaultErrors {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			for _, v := range e.VaultErrors[p] {
				fmt.Fprintf(&b, "\n  %s\t(%s)\n    → %v", v.Ref.URI, p, v.Err)
			}
		}
	}

	return b.String()
}

func (e *UnresolvedVarsError) add(path string, vars []string) {
	if len(vars) == 0 {
		return
	}
	if e.Files == nil {
		e.Files = map[string][]string{}
	}
	e.Files[path] = append(e.Files[path], vars...)
}

func (e *UnresolvedVarsError) addVault(path string, errs []configloader.VaultRefError) {
	if len(errs) == 0 {
		return
	}
	if e.VaultErrors == nil {
		e.VaultErrors = map[string][]configloader.VaultRefError{}
	}
	e.VaultErrors[path] = append(e.VaultErrors[path], errs...)
}

func (e *UnresolvedVarsError) empty() bool {
	return e == nil || (len(e.Files) == 0 && len(e.VaultErrors) == 0)
}
