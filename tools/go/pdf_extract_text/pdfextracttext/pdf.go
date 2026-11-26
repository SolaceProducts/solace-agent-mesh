// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pdfextracttext

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"
)

var errEncrypted = errors.New("PDF is encrypted or password-protected")

var errStructuredTimeout = errors.New("PDF parser timeout")

const structuredExtractTimeout = 20 * time.Second

func extract(content []byte, maxPages int) (*ExtractResult, error) {
	res, parseErr := extractStructured(content, maxPages)
	if parseErr == nil && len(res.Pages) > 0 {
		return res, nil
	}
	if errors.Is(parseErr, errEncrypted) {
		return nil, parseErr
	}

	scanned := extractTextFromPDFRaw(content)
	if scanned != "" {
		numPages := 0
		if res != nil {
			numPages = res.NumPages
		}
		return &ExtractResult{
			Pages:    []Page{{Number: 0, Text: scanned}},
			NumPages: numPages,
		}, nil
	}

	if parseErr != nil && res == nil {
		return &ExtractResult{Pages: []Page{}, NumPages: 0}, nil
	}
	if res == nil {
		res = &ExtractResult{Pages: []Page{}}
	}
	if res.Pages == nil {
		res.Pages = []Page{}
	}
	return res, nil
}

type structuredResult struct {
	res *ExtractResult
	err error
}

func extractStructured(content []byte, maxPages int) (*ExtractResult, error) {
	ch := make(chan structuredResult, 1)
	go func() {
		var sr structuredResult
		defer func() {
			if r := recover(); r != nil {
				sr = structuredResult{err: fmt.Errorf("PDF parser panic: %v", r)}
			}
			ch <- sr
		}()
		sr.res, sr.err = structuredExtractBlocking(content, maxPages)
	}()

	select {
	case sr := <-ch:
		return sr.res, sr.err
	case <-time.After(structuredExtractTimeout):
		return nil, errStructuredTimeout
	}
}

func structuredExtractBlocking(content []byte, maxPages int) (*ExtractResult, error) {
	tmpFile, err := os.CreateTemp("", "sam-pdf-*.pdf")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	if _, err = tmpFile.Write(content); err != nil {
		_ = tmpFile.Close()
		return nil, fmt.Errorf("write temp file: %w", err)
	}
	_ = tmpFile.Close()

	f, r, openErr := pdf.Open(tmpFile.Name())
	if openErr != nil {
		if isEncryptedErr(openErr) {
			return nil, errEncrypted
		}
		return nil, fmt.Errorf("open PDF: %w", openErr)
	}
	defer func() { _ = f.Close() }()

	numPages := r.NumPage()
	out := &ExtractResult{NumPages: numPages, Pages: []Page{}}

	limit := numPages
	if maxPages > 0 && limit > maxPages {
		limit = maxPages
		out.Truncated = true
	}

	for i := 1; i <= limit; i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, perr := page.GetPlainText(nil)
		if perr != nil || text == "" {
			continue
		}
		out.Pages = append(out.Pages, Page{Number: i, Text: text})
	}

	return out, nil
}

func isEncryptedErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "encrypted") || strings.Contains(msg, "password")
}

func extractTextFromContentStream(stream string) string {
	var result strings.Builder
	i := 0
	for i < len(stream) {
		if stream[i] == '(' {
			end := findClosingParen(stream, i)
			if end > i {
				text := decodePDFString(stream[i+1 : end])
				result.WriteString(text)
				i = end + 1
				continue
			}
		}
		i++
	}
	return result.String()
}

func findClosingParen(s string, start int) int {
	depth := 1
	i := start + 1
	for i < len(s) && depth > 0 {
		switch s[i] {
		case '\\':
			i++
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

func decodePDFString(s string) string {
	var buf strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				buf.WriteByte('\n')
			case 'r':
				buf.WriteByte('\r')
			case 't':
				buf.WriteByte('\t')
			case '\\':
				buf.WriteByte('\\')
			case '(':
				buf.WriteByte('(')
			case ')':
				buf.WriteByte(')')
			default:
				buf.WriteByte(s[i+1])
			}
			i += 2
		} else {
			buf.WriteByte(s[i])
			i++
		}
	}
	return buf.String()
}

func extractTextFromPDFRaw(content []byte) string {
	s := string(content)
	var result strings.Builder

	for {
		idx := strings.Index(s, "stream\n")
		if idx < 0 {
			idx = strings.Index(s, "stream\r\n")
			if idx < 0 {
				break
			}
			idx += 8
		} else {
			idx += 7
		}
		end := strings.Index(s[idx:], "endstream")
		if end < 0 {
			break
		}
		stream := s[idx : idx+end]
		result.WriteString(extractTextFromContentStream(stream))
		s = s[idx+end:]
	}

	return result.String()
}
