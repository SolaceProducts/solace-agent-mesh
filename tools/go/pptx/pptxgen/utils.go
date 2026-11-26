// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"fmt"
	"strconv"
	"strings"
)

const emuPerInch = 914400

const emuPerPoint = 12700

func inchToEMU(inches float64) int {
	if inches >= 0 {
		return int(inches*emuPerInch + 0.5)
	}
	return int(inches*emuPerInch - 0.5)
}

func pointToEMU(points float64) int {
	if points >= 0 {
		return int(points*emuPerPoint + 0.5)
	}
	return int(points*emuPerPoint - 0.5)
}

func parseColor(c string) string {
	normalized, err := NormalizeColorHex(c)
	if err != nil {
		return ""
	}
	return normalized
}

func NormalizeColorHex(c string) (string, error) {
	trimmed := strings.TrimSpace(c)
	if trimmed == "" {
		return "", nil
	}
	hex := strings.TrimPrefix(trimmed, "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return "", fmt.Errorf("colour %q must be 3 or 6 hex digits, optionally prefixed with '#' (e.g. C0392B or #C0392B)", c)
	}
	for _, r := range hex {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return "", fmt.Errorf("colour %q contains non-hex character %q; use a hex value such as C0392B, not a colour name", c, string(r))
		}
	}
	return strings.ToUpper(hex), nil
}

func escapeXML(s string) string {
	if !needsXMLEscape(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch {
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '&':
			b.WriteString("&amp;")
		case r == '"':
			b.WriteString("&quot;")
		case r == '\'':
			b.WriteString("&apos;")
		case r < 0x20 && r != '\t' && r != '\n' && r != '\r':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func needsXMLEscape(s string) bool {
	for i := range len(s) {
		c := s[i]
		switch c {
		case '<', '>', '&', '"', '\'':
			return true
		}
		if c < 0x20 && c != '\t' && c != '\n' && c != '\r' {
			return true
		}
	}
	return false
}

const firstSlideLocalCNvPrID = 2

type idAllocator struct {
	next int
}

func newIDAllocator() *idAllocator { return &idAllocator{next: firstSlideLocalCNvPrID} }

func (a *idAllocator) Next() int {
	n := a.next
	a.next++
	return n
}

const xmlDeclaration = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

func writeXMLDecl(b *strings.Builder) { b.WriteString(xmlDeclaration) }

func fmtInt(n int) string { return strconv.Itoa(n) }

type relIDAllocator struct {
	next int
}

func newRelIDAllocator(start int) *relIDAllocator { return &relIDAllocator{next: start} }

func (a *relIDAllocator) Next() string {
	id := "rId" + strconv.Itoa(a.next)
	a.next++
	return id
}
