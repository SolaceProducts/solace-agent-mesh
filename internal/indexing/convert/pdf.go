// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package convert

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"
)

const structuredExtractTimeout = 20 * time.Second

func ConvertPDF(content []byte) (*Result, error) {
	pages, err := extractPDFPages(content)
	if err != nil {
		return nil, err
	}
	return resultFromPages(pages), nil
}

type pageEntry struct {
	number int
	text   string
}

func resultFromPages(pages []pageEntry) *Result {
	var (
		buf       bytes.Buffer
		citations []CitationRange
		charPos   int
	)
	for i, p := range pages {
		if p.text == "" {
			continue
		}
		loc := "unknown"
		if p.number > 0 {
			loc = fmt.Sprintf("physical_page_%d", p.number)
		}
		citations = append(citations, CitationRange{
			Location:  loc,
			CharStart: charPos,
			CharEnd:   charPos + len(p.text),
		})
		buf.WriteString(p.text)
		if i < len(pages)-1 {
			buf.WriteByte('\n')
			charPos += len(p.text) + 1
		} else {
			charPos += len(p.text)
		}
	}
	if len(citations) > 0 {
		citations[len(citations)-1].CharEnd = buf.Len()
	}
	return &Result{
		Text:         buf.String(),
		CitationType: "page",
		Citations:    citations,
	}
}

type pagesResult struct {
	pages []pageEntry
	err   error
}

func extractPDFPages(content []byte) ([]pageEntry, error) {
	ch := make(chan pagesResult, 1)
	go func() {
		var pr pagesResult
		defer func() {
			if r := recover(); r != nil {
				pr = pagesResult{err: fmt.Errorf("PDF parser panic: %v", r)}
			}
			ch <- pr
		}()
		pr.pages, pr.err = extractPDFPagesBlocking(content)
	}()

	select {
	case pr := <-ch:
		return pr.pages, pr.err
	case <-time.After(structuredExtractTimeout):
		return nil, fmt.Errorf("PDF parser timeout after %s", structuredExtractTimeout)
	}
}

func extractPDFPagesBlocking(content []byte) ([]pageEntry, error) {
	tmpFile, err := os.CreateTemp("", "sam-idx-*.pdf")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	if _, err = tmpFile.Write(content); err != nil {
		_ = tmpFile.Close()
		return nil, fmt.Errorf("write temp file: %w", err)
	}
	_ = tmpFile.Close()

	f, r, err := pdf.Open(tmpFile.Name())
	if err != nil {
		if isEncryptedErr(err) {
			return nil, fmt.Errorf("PDF is encrypted or password-protected")
		}
		return nil, fmt.Errorf("open PDF: %w", err)
	}
	defer func() { _ = f.Close() }()

	var pages []pageEntry
	for i := 1; i <= r.NumPage(); i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, perr := page.GetPlainText(nil)
		if perr != nil || text == "" {
			continue
		}
		pages = append(pages, pageEntry{number: i, text: text})
	}
	return pages, nil
}

func isEncryptedErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "encrypted") || strings.Contains(msg, "password")
}
