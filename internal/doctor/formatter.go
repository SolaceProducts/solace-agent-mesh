// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"fmt"
	"strings"
)

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
)

func colorize(text string, codes ...string) string {
	if len(codes) == 0 {
		return text
	}
	return strings.Join(codes, "") + text + ansiReset
}

func statusColors(s Status) []string {
	switch s {
	case StatusPass:
		return []string{ansiGreen}
	case StatusWarn:
		return []string{ansiYellow}
	case StatusFail:
		return []string{ansiRed}
	case StatusSkip:
		return []string{ansiDim}
	}
	return nil
}

func (d *Doctor) PrintReport(results []CheckResult, doctorCtx Context) {
	header := fmt.Sprintf("SAM Doctor v%s — Pre-flight Check", Version)
	if doctorCtx != "" {
		header += fmt.Sprintf(" [%s]", doctorCtx)
	}
	separator := strings.Repeat("=", len(header))
	_, _ = fmt.Fprintln(d.Out)
	_, _ = fmt.Fprintln(d.Out, header)
	_, _ = fmt.Fprintln(d.Out, separator)
	_, _ = fmt.Fprintln(d.Out)

	grouped := groupResults(results)
	d.printGrouped(grouped)

	_, _ = fmt.Fprintln(d.Out)
	_, _ = fmt.Fprintln(d.Out, separator)
	d.printSummary(grouped)
	_, _ = fmt.Fprintln(d.Out, separator)
}

func groupResults(results []CheckResult) map[Status][]CheckResult {
	out := map[Status][]CheckResult{
		StatusFail: nil,
		StatusWarn: nil,
		StatusSkip: nil,
		StatusPass: nil,
	}
	for _, r := range results {
		out[r.Status] = append(out[r.Status], r)
	}
	return out
}

func (d *Doctor) printGrouped(grouped map[Status][]CheckResult) {
	first := true
	for _, s := range []Status{StatusFail, StatusWarn, StatusSkip, StatusPass} {
		group := grouped[s]
		if len(group) == 0 {
			continue
		}
		if !first {
			_, _ = fmt.Fprintln(d.Out)
		}
		for _, r := range group {
			_, _ = fmt.Fprintln(d.Out, formatResultLine(r))
		}
		first = false
	}
}

func formatResultLine(r CheckResult) string {
	colors := statusColors(r.Status)
	label := colorize(fmt.Sprintf("[%-4s]", string(r.Status)), colors...)
	name := r.Name
	if r.Status == StatusFail {
		name = colorize(name, colors...)
	}
	line := fmt.Sprintf("%s  %s", label, name)
	if r.Detail != "" {
		line += fmt.Sprintf(" (%s)", r.Detail)
	}
	if r.Reason == "" {
		return line
	}
	parts := strings.Split(r.Reason, "; ")
	if len(parts) > 1 {
		var b strings.Builder
		b.WriteString(line)
		for _, p := range parts {
			pc := colors
			if strings.Contains(p, "[WARN]") {
				pc = statusColors(StatusWarn)
			}
			b.WriteString("\n           • ")
			b.WriteString(colorize(p, pc...))
		}
		return b.String()
	}
	return line + "\n         " + colorize(r.Reason, colors...)
}

func (d *Doctor) printSummary(grouped map[Status][]CheckResult) {
	fails := grouped[StatusFail]
	warns := grouped[StatusWarn]
	skips := grouped[StatusSkip]
	passes := grouped[StatusPass]

	if len(fails) == 0 && len(warns) == 0 {
		_, _ = fmt.Fprintln(d.Out, colorize("  ✔  All checks passed — ready to deploy.", ansiGreen, ansiBold))
	} else {
		if len(fails) > 0 {
			_, _ = fmt.Fprintln(d.Out, colorize(fmt.Sprintf("  ✘  %d %s — resolve before deploying.", len(fails), pluralize("FAIL", len(fails), "FAILS")), ansiRed, ansiBold))
		}
		if len(warns) > 0 {
			_, _ = fmt.Fprintln(d.Out, colorize(fmt.Sprintf("  ⚠  %d %s — review before deploying.", len(warns), pluralize("WARN", len(warns), "WARNS")), ansiYellow))
		}
	}
	if len(skips) > 0 {
		_, _ = fmt.Fprintf(d.Out, "  ·  %d %s — review before deploying.\n", len(skips), pluralize("SKIP", len(skips), "SKIPS"))
	}
	if len(passes) > 0 {
		_, _ = fmt.Fprintln(d.Out, colorize(fmt.Sprintf("  ✔  %d %s", len(passes), pluralize("PASS", len(passes), "PASSES")), ansiGreen))
	}
}

func pluralize(singular string, n int, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
