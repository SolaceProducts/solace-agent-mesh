// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package commentcheck

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const Message = "comments are not allowed: rename for intent, extract a named function, introduce a named constant, or state the rule as a test name"

type Violation struct {
	Path string
	Line int
	Text string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", v.Path, v.Line, Message, firstLine(v.Text))
}

type span struct {
	start, end int
	line       int
	text       string
}

type Language int

const (
	Unsupported Language = iota
	Go
	Shell
	Python
	SQL
)

func LanguageOf(path string) Language {
	switch filepath.Ext(path) {
	case ".go":
		return Go
	case ".sh", ".bash":
		return Shell
	case ".py":
		return Python
	case ".sql":
		return SQL
	}
	return Unsupported
}

func Check(path string, src []byte) ([]Violation, error) {
	spans, err := commentSpans(path, src)
	if err != nil {
		return nil, err
	}
	violations := make([]Violation, 0, len(spans))
	for _, s := range spans {
		violations = append(violations, Violation{Path: path, Line: s.line, Text: s.text})
	}
	return violations, nil
}

func Fix(path string, src []byte) ([]byte, error) {
	spans, err := commentSpans(path, src)
	if err != nil || len(spans) == 0 {
		return src, err
	}
	out := removeSpans(src, spans)
	if LanguageOf(path) != Go {
		return collapseBlankLines(out), nil
	}
	formatted, err := format.Source(out)
	if err != nil {
		return nil, fmt.Errorf("%s: format after removing comments: %w", path, err)
	}
	return formatted, nil
}

func commentSpans(path string, src []byte) ([]span, error) {
	switch LanguageOf(path) {
	case Go:
		return goSpans(path, src)
	case Shell:
		return shellSpans(src), nil
	case Python:
		return pythonSpans(src), nil
	case SQL:
		return sqlSpans(src), nil
	}
	return nil, nil
}

var goDirective = regexp.MustCompile(`^//([a-z0-9]+:[a-z0-9]|line |export |extern |nolint\b| \+build )`)

func goSpans(path string, src []byte) ([]span, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if ast.IsGenerated(f) {
		return nil, nil
	}
	keep := cgoPreamble(f)
	if publicSDK.MatchString(filepath.ToSlash(path)) && !strings.HasSuffix(path, "_test.go") {
		for group := range exportedDocs(f) {
			keep[group] = true
		}
	}
	var spans []span
	for _, group := range f.Comments {
		if keep[group] {
			continue
		}
		for _, c := range group.List {
			if goDirective.MatchString(c.Text) {
				continue
			}
			start := fset.Position(c.Pos())
			spans = append(spans, span{
				start: start.Offset,
				end:   fset.Position(c.End()).Offset,
				line:  start.Line,
				text:  c.Text,
			})
		}
	}
	return spans, nil
}

func cgoPreamble(f *ast.File) map[*ast.CommentGroup]bool {
	keep := map[*ast.CommentGroup]bool{}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.IMPORT {
			continue
		}
		for _, spec := range gen.Specs {
			imp := spec.(*ast.ImportSpec)
			if imp.Path.Value != `"C"` {
				continue
			}
			if gen.Doc != nil {
				keep[gen.Doc] = true
			}
			if imp.Doc != nil {
				keep[imp.Doc] = true
			}
		}
	}
	return keep
}

var publicSDK = regexp.MustCompile(`(^|/)pkg/samtoolsdk/`)

func exportedDocs(f *ast.File) map[*ast.CommentGroup]bool {
	docs := map[*ast.CommentGroup]bool{}
	add := func(g *ast.CommentGroup) {
		if g != nil {
			docs[g] = true
		}
	}
	add(f.Doc)
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.IsExported() && (d.Recv == nil || ast.IsExported(receiverTypeName(d.Recv.List[0].Type))) {
				add(d.Doc)
			}
		case *ast.GenDecl:
			exportedSpecDocs(d, add)
		}
	}
	return docs
}

func exportedSpecDocs(d *ast.GenDecl, add func(*ast.CommentGroup)) {
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if !s.Name.IsExported() {
				continue
			}
			add(s.Doc)
			if len(d.Specs) == 1 {
				add(d.Doc)
			}
			exportedMemberDocs(s.Type, add)
		case *ast.ValueSpec:
			for _, name := range s.Names {
				if name.IsExported() {
					add(s.Doc)
					add(s.Comment)
					if len(d.Specs) == 1 {
						add(d.Doc)
					}
				}
			}
		}
	}
}

func exportedMemberDocs(expr ast.Expr, add func(*ast.CommentGroup)) {
	var fields *ast.FieldList
	switch t := expr.(type) {
	case *ast.StructType:
		fields = t.Fields
	case *ast.InterfaceType:
		fields = t.Methods
	default:
		return
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			if name.IsExported() {
				add(field.Doc)
				add(field.Comment)
			}
		}
	}
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.IndexExpr:
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	}
	return ""
}

var (
	shellDirective  = regexp.MustCompile(`^#(!|\s*shellcheck\s)`)
	pythonDirective = regexp.MustCompile(`^#(!|\s*(noqa|type:|pyright:|mypy:|pylint:|fmt:|isort:|ruff:)|.*-\*-.*coding)`)
	sqlDirective    = regexp.MustCompile(`^--\s*\+goose\b`)
)

func shellSpans(src []byte) []span {
	var spans []span
	offset := 0
	heredocEnd := ""
	var quote byte
	for i, line := range splitLines(src) {
		lineStart := offset
		offset += len(line)
		text := strings.TrimRight(string(line), "\r\n")
		if heredocEnd != "" {
			if strings.TrimLeft(text, "\t") == heredocEnd {
				heredocEnd = ""
			}
			continue
		}
		idx, nextQuote := shellCommentStart(text, quote)
		quote = nextQuote
		code := text
		if idx >= 0 {
			comment := text[idx:]
			if (i != 0 || !strings.HasPrefix(comment, "#!")) && !shellDirective.MatchString(comment) {
				spans = append(spans, span{start: lineStart + idx, end: lineStart + len(text), line: i + 1, text: comment})
			}
			code = text[:idx]
		}
		if quote == 0 {
			heredocEnd = heredocTerminator(code)
		}
	}
	return spans
}

var heredocPattern = regexp.MustCompile(`<<-?\s*(['"]?)([A-Za-z_][A-Za-z0-9_]*)['"]?`)

func heredocTerminator(code string) string {
	m := heredocPattern.FindStringSubmatch(code)
	if m == nil || strings.Contains(code, "<<<") {
		return ""
	}
	return m[2]
}

func shellCommentStart(line string, quote byte) (int, byte) {
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if ch == '\\' && quote != '\'' {
			i++
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			quote = ch
		case '#':
			if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' || line[i-1] == ';' {
				return i, 0
			}
		}
	}
	return -1, quote
}

func pythonSpans(src []byte) []span {
	var spans []span
	lines := splitLines(src)
	offset := 0
	var triple string
	for i, line := range lines {
		lineStart := offset
		offset += len(line)
		text := strings.TrimRight(string(line), "\r\n")
		idx, nextTriple := pythonCommentStart(text, triple)
		triple = nextTriple
		if idx < 0 {
			continue
		}
		comment := text[idx:]
		if (i == 0 && strings.HasPrefix(comment, "#!")) || pythonDirective.MatchString(comment) {
			continue
		}
		spans = append(spans, span{start: lineStart + idx, end: lineStart + len(text), line: i + 1, text: comment})
	}
	return spans
}

func pythonCommentStart(line, triple string) (int, string) {
	i := 0
	for i < len(line) {
		if triple != "" {
			end := strings.Index(line[i:], triple)
			if end < 0 {
				return -1, triple
			}
			i += end + 3
			triple = ""
			continue
		}
		ch := line[i]
		switch {
		case ch == '#':
			return i, ""
		case strings.HasPrefix(line[i:], `"""`) || strings.HasPrefix(line[i:], `'''`):
			triple = line[i : i+3]
			i += 3
		case ch == '\'' || ch == '"':
			i++
			for i < len(line) && line[i] != ch {
				if line[i] == '\\' {
					i++
				}
				i++
			}
			i++
		default:
			i++
		}
	}
	return -1, triple
}

func sqlSpans(src []byte) []span {
	var spans []span
	s := string(src)
	line := 1
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\n':
			line++
		case s[i] == '\'':
			for i++; i < len(s) && s[i] != '\''; i++ {
				if s[i] == '\n' {
					line++
				}
			}
		case strings.HasPrefix(s[i:], "$$"):
			end := strings.Index(s[i+2:], "$$")
			if end < 0 {
				return spans
			}
			line += strings.Count(s[i:i+2+end+2], "\n")
			i += 2 + end + 1
		case strings.HasPrefix(s[i:], "--"):
			end := strings.IndexByte(s[i:], '\n')
			if end < 0 {
				end = len(s) - i
			}
			comment := s[i : i+end]
			if !sqlDirective.MatchString(comment) {
				spans = append(spans, span{start: i, end: i + end, line: line, text: comment})
			}
			i += end - 1
		case strings.HasPrefix(s[i:], "/*"):
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return spans
			}
			comment := s[i : i+2+end+2]
			spans = append(spans, span{start: i, end: i + len(comment), line: line, text: comment})
			line += strings.Count(comment, "\n")
			i += len(comment) - 1
		}
	}
	return spans
}

func removeSpans(src []byte, spans []span) []byte {
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	var out bytes.Buffer
	cursor := 0
	for _, s := range spans {
		start, end := s.start, s.end
		lineStart := bytes.LastIndexByte(src[:start], '\n') + 1
		lineEnd := len(src)
		if nl := bytes.IndexByte(src[end:], '\n'); nl >= 0 {
			lineEnd = end + nl
		}
		before := src[lineStart:start]
		after := src[end:lineEnd]
		switch {
		case isBlank(before) && isBlank(after):
			start = lineStart
			end = min(lineEnd+1, len(src))
		case isBlank(after):
			start = lineStart + len(bytes.TrimRight(before, " \t"))
			end = lineEnd
		case start > 0 && end < len(src) && src[start-1] == ' ' && src[end] == ' ':
			end++
		}
		if start < cursor {
			start = cursor
		}
		out.Write(src[cursor:start])
		cursor = max(cursor, end)
	}
	out.Write(src[cursor:])
	return out.Bytes()
}

func collapseBlankLines(src []byte) []byte {
	lines := splitLines(src)
	var out bytes.Buffer
	blank := 0
	for i, line := range lines {
		if isBlank(bytes.TrimRight(line, "\r\n")) {
			blank++
			if blank > 1 || out.Len() == 0 && i > 0 {
				continue
			}
		} else {
			blank = 0
		}
		out.Write(line)
	}
	return out.Bytes()
}

func splitLines(src []byte) [][]byte {
	return bytes.SplitAfter(src, []byte("\n"))
}

func isBlank(b []byte) bool {
	return len(bytes.TrimSpace(b)) == 0
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	const limit = 80
	runes := []rune(line)
	if len(runes) > limit {
		return string(runes[:limit]) + "..."
	}
	return line
}
