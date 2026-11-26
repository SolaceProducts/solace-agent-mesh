// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"fmt"
	"io"
	"strings"
)

const platformSection = "platform"

type platformChange int

const (
	platformUnchanged platformChange = iota
	platformUpdated
	platformDeleted
)

func (c platformChange) marker() string {
	switch c {
	case platformUpdated:
		return "~"
	case platformDeleted:
		return "-"
	default:
		return "="
	}
}

func (c platformChange) color() string {
	switch c {
	case platformUpdated:
		return ansiYellow
	case platformDeleted:
		return ansiRed
	default:
		return ansiDim
	}
}

type platformRow struct {
	name   string
	verb   string
	detail string
	change platformChange
}

func (r platformRow) label() string {
	if r.detail == "" {
		return r.verb
	}
	return r.verb + " (" + r.detail + ")"
}

func roleNameList(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

func defaultRolesRow(p *defaultRolesPlan) (platformRow, bool) {
	if p == nil || p.Verb == defaultRolesVerbNone {
		return platformRow{}, false
	}
	row := platformRow{name: "defaultRoles", verb: string(p.Verb)}
	if p.Verb == defaultRolesVerbPut {
		row.change = platformUpdated
		row.detail = roleNameList(p.CurrentNames) + " -> " + roleNameList(p.RoleNames)
	}
	return row, true
}

func webuiSettingsRow(p *webuiSettingsPlan) (platformRow, bool) {
	if p == nil || p.Verb == webuiSettingsVerbNone {
		return platformRow{}, false
	}
	row := platformRow{name: "webuiSettings", verb: string(p.Verb)}
	switch p.Verb {
	case webuiSettingsVerbPut:
		row.change = platformUpdated
		if webuiSettingsPruneOnly(p) {
			row.change = platformDeleted
		}
		changed := webuiSettingsDiff(p.Current, p.Desired)
		if len(changed) == 0 {
			row.detail = "(no field diff)"
		} else {
			row.detail = strings.Join(changed, ", ")
		}
	case webuiSettingsVerbDelete:
		row.change = platformDeleted
		row.detail = strings.Join(webuiSettingsOverrideNames(p.Current), ", ")
	}
	return row, true
}

func profileProviderRow(p *profileProviderPlan) (platformRow, bool) {
	if p == nil || p.Verb == profileProviderVerbNone {
		return platformRow{}, false
	}
	row := platformRow{name: "profileProvider", verb: string(p.Verb)}
	switch p.Verb {
	case profileProviderVerbPut:
		row.change = platformUpdated
		row.detail = fmt.Sprintf("toolset: %s, claim: %s", p.ToolsetName, p.LookupKey)
	case profileProviderVerbDelete:
		row.change = platformDeleted
	}
	return row, true
}

func platformRows(dr *defaultRolesPlan, pp *profileProviderPlan, ws *webuiSettingsPlan) []platformRow {
	var rows []platformRow
	if row, ok := defaultRolesRow(dr); ok {
		rows = append(rows, row)
	}
	if row, ok := profileProviderRow(pp); ok {
		rows = append(rows, row)
	}
	if row, ok := webuiSettingsRow(ws); ok {
		rows = append(rows, row)
	}
	return rows
}

func renderPlatformPlan(w io.Writer, dr *defaultRolesPlan, pp *profileProviderPlan, ws *webuiSettingsPlan, opts FormatOptions) {
	rows := platformRows(dr, pp, ws)
	if len(rows) == 0 {
		return
	}

	var updates, deletes, unchanged int
	_, _ = fmt.Fprintf(w, "%s/\n", platformSection)
	for _, r := range rows {
		switch r.change {
		case platformUpdated:
			updates++
		case platformDeleted:
			deletes++
		default:
			unchanged++
		}
		label := r.label()
		if r.change == platformDeleted && !opts.Prune {
			label += needsPruneSuffix
		}
		_, _ = fmt.Fprintf(w, rowSimple,
			color(opts, r.change.color(), r.change.marker()+" "+padName(r.name)),
			color(opts, r.change.color(), label))
	}
	_, _ = fmt.Fprintf(w, totalsLine, len(rows), platformSection, 0, updates, deletes, unchanged)
}

func renderPlatformApply(w io.Writer, dr *defaultRolesOutcome, pp *profileProviderOutcome, ws *webuiSettingsOutcome, opts FormatOptions) {
	type line struct {
		name   string
		text   string
		marker string
		color  string
		err    error
	}
	var lines []line

	if dr != nil {
		switch {
		case dr.Applied != nil:
			lines = append(lines, line{"defaultRoles", string(dr.Applied.Verb) + appliedSuffix, "~", ansiGreen, nil})
		case dr.Skipped != nil:
			lines = append(lines, line{"defaultRoles", platformSkipLabel(dr.Skipped), "=", ansiDim, nil})
		}
		for _, f := range dr.Failures {
			lines = append(lines, line{"defaultRoles", string(f.Verb) + failedSuffix, "!", ansiRed, f.Err})
		}
	}
	if pp != nil {
		switch {
		case pp.Applied != nil:
			lines = append(lines, line{"profileProvider", string(pp.Applied.Verb) + appliedSuffix, "~", ansiGreen, nil})
		case pp.Skipped != nil:
			label := string(pp.Skipped.Verb)
			if pp.Skipped.Verb == profileProviderVerbDelete {
				label += " " + skipPruneLabel
			}
			lines = append(lines, line{"profileProvider", label, "=", ansiDim, nil})
		}
		for _, f := range pp.Failures {
			lines = append(lines, line{"profileProvider", string(f.Verb) + failedSuffix, "!", ansiRed, f.Err})
		}
	}
	if ws != nil {
		switch {
		case ws.Applied != nil:
			lines = append(lines, line{"webuiSettings", string(ws.Applied.Verb) + appliedSuffix, "~", ansiGreen, nil})
		case ws.Skipped != nil:
			label := string(ws.Skipped.Verb)
			if ws.Skipped.Verb == webuiSettingsVerbDelete {
				label += " " + skipPruneLabel
			}
			lines = append(lines, line{"webuiSettings", label, "=", ansiDim, nil})
		}
		for _, f := range ws.Failures {
			lines = append(lines, line{"webuiSettings", string(f.Verb) + failedSuffix, "!", ansiRed, f.Err})
		}
	}
	if len(lines) == 0 {
		return
	}

	_, _ = fmt.Fprintf(w, appliedHeading, platformSection)
	for _, l := range lines {
		if l.err != nil {
			_, _ = fmt.Fprintf(w, rowWithError,
				color(opts, l.color, l.marker+" "+padName(l.name)), color(opts, l.color, l.text), l.err)
			continue
		}
		_, _ = fmt.Fprintf(w, rowSimple,
			color(opts, l.color, l.marker+" "+padName(l.name)), color(opts, l.color, l.text))
	}
}

func platformSkipLabel(r *defaultRolesOpResult) string {
	if r.Reason == skipReasonMiniIDP {
		return skipMiniIDPLabel
	}
	return string(r.Verb)
}
