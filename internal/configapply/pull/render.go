// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"fmt"
	"io"
	"slices"
)

func RenderResult(w io.Writer, r *Result) {
	if r == nil {
		return
	}

	first := true
	pf(w, "Wrote ")
	for _, k := range pulledKinds {
		n := r.Counts[k]
		if n == 0 {
			continue
		}
		if !first {
			pf(w, ", ")
		}
		pf(w, "%d %s%s", n, k, plural(n))
		first = false
	}
	if first {
		pf(w, "0 resources")
	}
	pf(w, " to %s\n", r.OutputDir)

	if r.ManifestPath != "" {
		pf(w, "Manifest: %s\n", r.ManifestPath)
	}

	skippedKinds := make([]string, 0, len(r.Skipped))
	for k, n := range r.Skipped {
		if n > 0 {
			skippedKinds = append(skippedKinds, k)
		}
	}
	slices.Sort(skippedKinds)
	for _, k := range skippedKinds {
		pf(w, "Skipped %d %s%s — pull for this kind is not implemented in v1\n", r.Skipped[k], k, plural(r.Skipped[k]))
	}

	if len(r.SecretPlaceholders) > 0 {
		pf(w, "Secret placeholders (set these env vars before re-applying):\n")
		for _, s := range r.SecretPlaceholders {
			pf(w, "  - %s.%s.%s -> %s\n", s.Kind, s.ResourceName, s.FieldPath, s.Placeholder)
		}
	}

	for _, msg := range r.Warnings {
		pf(w, "warning: %s\n", msg)
	}
}

func pf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}
