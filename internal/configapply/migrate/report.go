// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migrate

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

type Result struct {
	Source   string   `json:"source"`
	Target   string   `json:"target,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Name     string   `json:"name,omitempty"`
	Messages []string `json:"messages,omitempty"`
}

type Report struct {
	Migrated []Result `json:"migrated"`
	Warnings []Result `json:"warnings"`
	Skipped  []Result `json:"skipped"`
	Errored  []Result `json:"errored"`
}

func (r *Report) AddMigrated(source, target, kind, name string) {
	r.Migrated = append(r.Migrated, Result{
		Source: source, Target: target, Kind: kind, Name: name,
	})
}

func (r *Report) AddWarning(source, target, kind, name string, messages []string) {
	r.Warnings = append(r.Warnings, Result{
		Source: source, Target: target, Kind: kind, Name: name, Messages: append([]string(nil), messages...),
	})
}

func (r *Report) AddSkipped(source, kind, reason string) {
	r.Skipped = append(r.Skipped, Result{
		Source: source, Kind: kind, Messages: []string{reason},
	})
}

func (r *Report) AddError(source string, err error) {
	r.Errored = append(r.Errored, Result{
		Source: source, Messages: []string{err.Error()},
	})
}

func (r *Report) HasFailures() bool {
	return len(r.Errored) > 0
}

func (r *Report) Sort() {
	for _, b := range []*[]Result{&r.Migrated, &r.Warnings, &r.Skipped, &r.Errored} {
		sort.Slice(*b, func(i, j int) bool { return (*b)[i].Source < (*b)[j].Source })
	}
}

func (r *Report) WriteText(w io.Writer) {
	r.Sort()
	_, _ = fmt.Fprintf(w, "Migration summary: %d migrated, %d with warnings, %d skipped, %d errored\n",
		len(r.Migrated), len(r.Warnings), len(r.Skipped), len(r.Errored))

	if len(r.Migrated) > 0 {
		_, _ = fmt.Fprintln(w, "\nMigrated:")
		for _, m := range r.Migrated {
			_, _ = fmt.Fprintf(w, "  %s → %s (%s/%s)\n", m.Source, m.Target, m.Kind, m.Name)
		}
	}
	if len(r.Warnings) > 0 {
		_, _ = fmt.Fprintln(w, "\nWith warnings:")
		for _, m := range r.Warnings {
			_, _ = fmt.Fprintf(w, "  %s → %s (%s/%s)\n", m.Source, m.Target, m.Kind, m.Name)
			for _, msg := range m.Messages {
				_, _ = fmt.Fprintf(w, "    - %s\n", msg)
			}
		}
	}
	if len(r.Skipped) > 0 {
		_, _ = fmt.Fprintln(w, "\nSkipped:")
		for _, m := range r.Skipped {
			reason := "skipped"
			if len(m.Messages) > 0 {
				reason = m.Messages[0]
			}
			kind := m.Kind
			if kind == "" {
				kind = "unknown"
			}
			_, _ = fmt.Fprintf(w, "  %s (%s): %s\n", m.Source, kind, reason)
		}
	}
	if len(r.Errored) > 0 {
		_, _ = fmt.Fprintln(w, "\nErrored:")
		for _, m := range r.Errored {
			msg := "unknown error"
			if len(m.Messages) > 0 {
				msg = m.Messages[0]
			}
			_, _ = fmt.Fprintf(w, "  %s: %s\n", m.Source, msg)
		}
	}
}

func (r *Report) ToJSON() ([]byte, error) {
	r.Sort()
	return json.MarshalIndent(r, "", "  ")
}
