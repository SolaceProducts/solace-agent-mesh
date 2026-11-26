// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

const citationID = `(?:s\d+r\d+|idx\d+r\d+|research\d+)`

const (
	sourcesHeading      = "*Sources*"
	maxSourceTitleRunes = 80
	maxLinkTargetRunes  = 2000
	maxLinkRunes        = maxLinkTargetRunes + maxSiteRunes + 3
	maxSiteRunes        = 253
	placeholderEdge     = "\x01"
	unwritableSiteChars = "*~`<>|"
	linkStart           = "<http"
)

const (
	zeroWidthNonJoiner          = 0x200c
	zeroWidthJoiner             = 0x200d
	brailleBlank                = 0x2800
	supplementaryVariationFirst = 0xe0100
	supplementaryVariationLast  = 0xe01ef
)

var (
	citationRE        = regexp.MustCompile(`([ \t]?)\[?\[cite:([^\]\n]*)\]\]?`)
	citationIDRE      = regexp.MustCompile(citationID)
	partialCitationRE = regexp.MustCompile(`[ \t]?(?:\[\[?cite:[^\]\n]*|\[\[(?:c|ci|cit|cite)?)$`)
	placeholderRE     = regexp.MustCompile(placeholderEdge + `C(\d+)` + placeholderEdge)
	chatMarkupRE      = regexp.MustCompile("[*_~`<>|]+")
	bareAddressRE     = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*://|www\.[\p{L}\p{N}])\S*`)
	textStripper      = strings.NewReplacer("\x00", "", placeholderEdge, "")
)

type citationLink struct {
	target, site, title string
}

type citationBook struct {
	mu        sync.Mutex
	links     map[string]citationLink
	pages     map[string]string
	ambiguous map[string]bool
}

type citationsAdded struct {
	changed  int
	collided int
}

func (b *citationBook) add(sources []entrypoint.CitationSource) citationsAdded {
	var added citationsAdded
	if b == nil || len(sources) == 0 {
		return added
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range sources {
		if b.ambiguous[s.ID] {
			continue
		}
		page := pageKey(s.URL)
		if known := b.pages[s.ID]; known != "" && page != "" && page != known {
			if b.ambiguous == nil {
				b.ambiguous = make(map[string]bool)
			}
			b.ambiguous[s.ID] = true
			added.changed++
			added.collided++
			continue
		}
		if page != "" && b.pages[s.ID] == "" {
			if b.pages == nil {
				b.pages = make(map[string]string)
			}
			b.pages[s.ID] = page
		}
		link, ok := presentableCitation(s)
		if !ok {
			continue
		}
		known, heard := b.links[s.ID]
		switch {
		case !heard:
			if b.links == nil {
				b.links = make(map[string]citationLink)
			}
			b.links[s.ID] = link
			added.changed++
		case known.title == "" && link.title != "":
			known.title = link.title
			b.links[s.ID] = known
			added.changed++
		}
	}
	return added
}

func pageKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if port := u.Port(); port != "" && port != defaultPort[u.Scheme] {
		host += ":" + port
	}
	query := u.RawQuery
	if values, err := url.ParseQuery(query); err == nil {
		query = values.Encode()
	}
	path := u.RawPath
	if path == "" {
		path = u.EscapedPath()
	}
	return host + strings.TrimSuffix(canonicalPath(path), "/") + "?" + query
}

var defaultPort = map[string]string{"http": "80", "https": "443"}

func canonicalPath(escaped string) string {
	segments := strings.Split(escaped, "/")
	for i, segment := range segments {
		if plain, err := url.PathUnescape(segment); err == nil {
			segments[i] = url.PathEscape(plain)
		}
	}
	return strings.Join(segments, "/")
}

func (b *citationBook) link(id string) (citationLink, bool) {
	if b == nil {
		return citationLink{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ambiguous[id] {
		return citationLink{}, false
	}
	link, ok := b.links[id]
	return link, ok
}

func presentableCitation(s entrypoint.CitationSource) (citationLink, bool) {
	u, err := url.Parse(strings.TrimSpace(s.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return citationLink{}, false
	}
	u.User = nil
	site := strings.TrimPrefix(u.Hostname(), "www.")
	target := chatLinkTarget(u.String())
	if strings.ContainsAny(site, unwritableSiteChars) || strings.IndexFunc(site, func(r rune) bool { return unsafeRune(r) || unicode.IsSpace(r) }) >= 0 ||
		utf8.RuneCountInString(site) > maxSiteRunes || utf8.RuneCountInString(target) > maxLinkTargetRunes {
		return citationLink{}, false
	}
	return citationLink{target: target, site: site, title: sourceTitle(s.Title)}, true
}

type citationNumbers struct {
	cited  []citationLink
	number map[string]int
}

func (n *citationNumbers) numberOf(link citationLink) int {
	if num, ok := n.number[link.target]; ok {
		return num
	}
	if n.number == nil {
		n.number = make(map[string]int)
	}
	n.cited = append(n.cited, link)
	n.number[link.target] = len(n.cited)
	return len(n.cited)
}

type citedAnswer struct {
	text  string
	cited []citationLink
}

func (b *citationBook) renderAnswer(text string) string {
	cited := b.resolve(text)
	return cited.finish(renderForChat(cited.text))
}

func (b *citationBook) renderFile(text string, markdown bool) string {
	cited := b.resolveMarkers(text, false)
	if len(cited.cited) == 0 {
		return text
	}
	var out strings.Builder
	out.WriteString(strings.TrimRight(cited.fileNumbers(markdown), "\n"))
	if markdown {
		out.WriteString("\n\n## Sources\n")
	} else {
		out.WriteString("\n\nSources\n")
	}
	for i, link := range cited.cited {
		number := strconv.Itoa(i + 1)
		if markdown {
			out.WriteString("\n" + number + ". <" + markdownLinkTarget(link.target) + ">")
		} else {
			out.WriteString("\n[" + number + "] " + link.target)
		}
		if link.title != "" {
			title := link.title
			if markdown {
				title = markdownTitleEscaper.Replace(title)
			}
			out.WriteString(" — " + title)
		}
	}
	out.WriteString("\n")
	return out.String()
}

func (c citedAnswer) fileNumbers(markdown bool) string {
	var out strings.Builder
	written := 0
	for _, at := range placeholderRE.FindAllStringSubmatchIndex(c.text, -1) {
		out.WriteString(c.text[written:at[0]])
		written = at[1]
		num, err := strconv.Atoi(c.text[at[2]:at[3]])
		if err != nil || num < 1 || num > len(c.cited) {
			continue
		}
		label := "[" + strconv.Itoa(num) + "]"
		if !markdown || insideCodeSpan(c.text[:at[0]]) {
			out.WriteString(label)
			continue
		}
		out.WriteString(label + "(<" + markdownLinkTarget(c.cited[num-1].target) + ">)")
	}
	out.WriteString(c.text[written:])
	return out.String()
}

var markdownTitleEscaper = strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`)

func markdownLinkTarget(target string) string {
	return strings.ReplaceAll(target, `\`, "%5C")
}

func (b *citationBook) resolve(text string) citedAnswer {
	return b.resolveMarkers(text, true)
}

func (b *citationBook) resolveMarkers(text string, streaming bool) citedAnswer {
	text = textStripper.Replace(text)
	if !strings.Contains(text, "[") {
		return citedAnswer{text: text}
	}
	var numbers citationNumbers
	parts := splitCodeBlocks(text)
	for i := 0; i < len(parts); i += 2 {
		parts[i] = citationRE.ReplaceAllStringFunc(parts[i], func(marker string) string {
			return b.numberMarker(marker, citationRE.FindStringSubmatch(marker), &numbers)
		})
		parts[i] = strings.ReplaceAll(parts[i], placeholderEdge+placeholderEdge, placeholderEdge+" "+placeholderEdge)
	}
	if streaming {
		last := len(parts) - 1
		parts[last] = partialCitationRE.ReplaceAllString(parts[last], "")
	}
	return citedAnswer{text: strings.Join(parts, ""), cited: numbers.cited}
}

func (b *citationBook) numberMarker(marker string, match []string, numbers *citationNumbers) string {
	ids := citationIDRE.FindAllString(match[2], -1)
	if len(ids) == 0 {
		return marker
	}
	var numbered []string
	seen := make(map[int]bool)
	for _, id := range ids {
		link, ok := b.link(id)
		if !ok {
			continue
		}
		num := numbers.numberOf(link)
		if seen[num] {
			continue
		}
		seen[num] = true
		numbered = append(numbered, placeholderEdge+"C"+strconv.Itoa(num)+placeholderEdge)
	}
	if len(numbered) == 0 {
		return ""
	}
	return match[1] + strings.Join(numbered, " ")
}

func (c citedAnswer) finish(rendered string) string {
	if len(c.cited) == 0 {
		return rendered
	}
	parts := splitCodeBlocks(rendered)
	for i, part := range parts {
		parts[i] = c.writeNumbers(part, i%2 == 1)
	}
	return strings.Join(parts, "") + c.sourceList()
}

func (c citedAnswer) writeNumbers(part string, code bool) string {
	var out strings.Builder
	written := 0
	for _, at := range placeholderRE.FindAllStringSubmatchIndex(part, -1) {
		out.WriteString(part[written:at[0]])
		written = at[1]
		num, err := strconv.Atoi(part[at[2]:at[3]])
		if err != nil || num < 1 || num > len(c.cited) {
			continue
		}
		label := "[" + strconv.Itoa(num) + "]"
		if code || insideCodeSpan(part[:at[0]]) || insideLink(part[:at[0]]) {
			out.WriteString(label)
			continue
		}
		out.WriteString("<" + c.cited[num-1].target + "|" + label + ">")
	}
	out.WriteString(part[written:])
	return out.String()
}

func insideCodeSpan(before string) bool {
	line := before[strings.LastIndex(before, "\n")+1:]
	return strings.Count(line, "`")%2 == 1
}

func insideLink(before string) bool {
	return strings.LastIndex(before, linkStart) > strings.LastIndex(before, ">")
}

func (c citedAnswer) sourceList() string {
	var b strings.Builder
	b.WriteString("\n\n" + sourcesHeading)
	for i, link := range c.cited {
		b.WriteString("\n[" + strconv.Itoa(i+1) + "] <" + link.target + "|" + link.site + ">")
		if link.title != "" {
			b.WriteString(" — " + link.title)
		}
	}
	return b.String()
}

func plainChatText(raw string) string {
	raw = chatMarkupRE.ReplaceAllString(raw, " ")
	raw = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case unsafeRune(r):
			return -1
		}
		return r
	}, raw)
	return strings.Join(strings.Fields(raw), " ")
}

func unsafeRune(r rune) bool {
	switch {
	case r == zeroWidthNonJoiner || r == zeroWidthJoiner:
		return false
	case r == brailleBlank, r >= supplementaryVariationFirst && r <= supplementaryVariationLast:
		return true
	}
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r)
}

func sourceTitle(raw string) string {
	raw = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case unsafeRune(r):
			return -1
		}
		return r
	}, raw)
	runes := []rune(plainChatText(bareAddressRE.ReplaceAllString(raw, " ")))
	if len(runes) <= maxSourceTitleRunes {
		return string(runes)
	}
	return strings.TrimSpace(string(runes[:maxSourceTitleRunes])) + "…"
}

func chatLinkTarget(raw string) string {
	return strings.NewReplacer("<", "%3C", ">", "%3E", "|", "%7C", " ", "%20").Replace(raw)
}
