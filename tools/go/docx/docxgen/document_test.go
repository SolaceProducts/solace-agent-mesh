// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docxgen

import (
	"bytes"
	"strings"
	"testing"
)

func kitchenSink(t *testing.T) *Document {
	t.Helper()
	d := New()
	d.SetMetadata("Title", "Author", "Subject")
	mustAdd(t, "heading", d.AddHeading(1, "One"))
	mustAdd(t, "toc", d.AddTableOfContents("Contents", "1-3"))
	mustAdd(t, "page break", d.AddPageBreak())
	mustAdd(t, "heading 2", d.AddHeading(2, "Two"))
	mustAdd(t, "paragraph", d.AddParagraph([]Run{
		{Text: "plain "},
		{Text: "bold", Bold: true},
		{Text: " and ", Italic: true},
		{Text: "coloured", ColorHex: "C0392B", Underline: true},
	}, AlignCenter))
	mustAdd(t, "bullets", d.AddBulletList([]string{"a", "b"}))
	mustAdd(t, "numbers", d.AddNumberedList([]string{"1st", "2nd"}))
	mustAdd(t, "second numbers", d.AddNumberedList([]string{"restarts"}))
	mustAdd(t, "table", d.AddTable([]string{"h1", "h2"}, [][]string{{"a"}, {"b", "c"}}, TableOptions{HeaderBold: true}))
	mustAdd(t, "image 1", d.AddImage(tinyPNG(t), "png", EMUPerInch, EMUPerInch, ""))
	mustAdd(t, "image 2", d.AddImage(tinyPNG(t), "png", EMUPerInch*2, EMUPerInch, AlignRight))
	return d
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	return []byte{0x89, 'P', 'N', 'G', 0, 0, 0, 0}
}

func mustAdd(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("add %s: %v", what, err)
	}
}

func TestDocument_EveryPrimitiveInOneDocument(t *testing.T) {
	data, err := kitchenSink(t).ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}

	for _, name := range []string{
		"[Content_Types].xml", "_rels/.rels", "docProps/core.xml", "docProps/app.xml",
		"word/document.xml", "word/styles.xml", "word/numbering.xml",
		"word/_rels/document.xml.rels",
	} {
		zipFile(t, data, name)
	}

	names := zipNames(t, data)
	for _, media := range []string{"word/media/image1.png", "word/media/image2.png"} {
		if !containsString(names, media) {
			t.Errorf("package missing %s; entries: %v", media, names)
		}
	}

	rels := string(zipFile(t, data, "word/_rels/document.xml.rels"))
	for _, rid := range []string{`Id="rId101"`, `Id="rId102"`} {
		if !strings.Contains(rels, rid) {
			t.Errorf("document.xml.rels missing %s:\n%s", rid, rels)
		}
	}
	numbering := string(zipFile(t, data, "word/numbering.xml"))
	for _, numID := range []string{`w:numId="1"`, `w:numId="2"`, `w:numId="3"`} {
		if !strings.Contains(numbering, numID) {
			t.Errorf("numbering.xml missing %s:\n%s", numID, numbering)
		}
	}
}

func TestAddParagraph_HandlesControlCharacters(t *testing.T) {
	d := New()
	if err := d.AddParagraph([]Run{{Text: "before\x00\x01\x08after\ttab\nnewline"}}, ""); err != nil {
		t.Fatalf("AddParagraph: %v", err)
	}
	data, err := d.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}

	doc := zipFile(t, data, "word/document.xml")
	for _, illegal := range []byte{0x00, 0x01, 0x08} {
		if bytes.IndexByte(doc, illegal) >= 0 {
			t.Errorf("raw control byte %#x survived into document.xml", illegal)
		}
	}
	if !strings.Contains(string(doc), "before") || !strings.Contains(string(doc), "after") {
		t.Errorf("surrounding text was lost:\n%s", doc)
	}
}

func TestToBytes_IsByteDeterministic(t *testing.T) {
	first, err := kitchenSink(t).ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	second, err := kitchenSink(t).ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("two identical documents produced different bytes (%d vs %d) — "+
			"something non-deterministic (a timestamp, or map iteration order) entered the package",
			len(first), len(second))
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
