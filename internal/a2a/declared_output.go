// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	WorkflowToolLeadFormat = "Invoke the %q workflow."
	PeerToolLeadFormat     = "Delegate a task to the %q agent."
)

const ToolSkillsLabel = "Skills:"

const DeclaredOutputLabel = "Declared output fields:"

const MaxDeclaredOutputBytes = 1024

const (
	maxDeclaredFieldNameRunes = 64
	maxDeclaredFieldTypeRunes = 32
)

type OutputField struct {
	Name string
	Type string
}

func FormatDeclaredOutput(fields []OutputField) string {
	sorted := slices.DeleteFunc(slices.Clone(fields), func(f OutputField) bool { return f.Name == "" })
	slices.SortFunc(sorted, func(a, b OutputField) int { return cmp.Compare(a.Name, b.Name) })

	omittedTailReserve := len(fmt.Sprintf(", and %d more.", len(sorted)))
	var b strings.Builder
	b.WriteString(DeclaredOutputLabel)
	written := 0
	for _, f := range sorted {
		if utf8.RuneCountInString(f.Name) > maxDeclaredFieldNameRunes {
			continue
		}
		entry := f.Name
		if t := f.Type; t != "" && utf8.RuneCountInString(t) <= maxDeclaredFieldTypeRunes {
			entry += " (" + t + ")"
		}
		sep := ", "
		if written == 0 {
			sep = " "
		}
		if written > 0 && b.Len()+len(sep)+len(entry)+omittedTailReserve > MaxDeclaredOutputBytes {
			break
		}
		b.WriteString(sep + entry)
		written++
	}
	if written == 0 {
		return ""
	}
	if omitted := len(sorted) - written; omitted > 0 {
		fmt.Fprintf(&b, ", and %d more", omitted)
	}
	b.WriteString(".")
	return b.String()
}

var toolDescriptionLead = leadPattern(WorkflowToolLeadFormat, PeerToolLeadFormat)

func leadPattern(formats ...string) *regexp.Regexp {
	alternatives := make([]string, 0, len(formats))
	for _, format := range formats {
		before, after, _ := strings.Cut(format, "%q")
		alternatives = append(alternatives, regexp.QuoteMeta(before)+".*?"+regexp.QuoteMeta(after))
	}
	return regexp.MustCompile(`(?i)^(?:` + strings.Join(alternatives, "|") + `)`)
}

func Declared(toolDescription string) string {
	desc := strings.TrimSpace(toolDescription)
	lead := toolDescriptionLead.FindString(desc)
	desc = strings.TrimSpace(strings.TrimPrefix(desc, lead))
	if isPeerLead(lead) {
		desc = strings.TrimSpace(withoutSkills(desc))
	}
	return desc
}

func isPeerLead(lead string) bool {
	before, _, _ := strings.Cut(PeerToolLeadFormat, "%q")
	return strings.HasPrefix(strings.ToLower(lead), strings.ToLower(before))
}

func withoutSkills(desc string) string {
	if strings.HasPrefix(desc, ToolSkillsLabel) && !strings.Contains(desc, ".") {
		return ""
	}
	i := strings.LastIndex(desc, " "+ToolSkillsLabel)
	if i < 0 || strings.Contains(desc[i:], ".") {
		return desc
	}
	return desc[:i]
}
