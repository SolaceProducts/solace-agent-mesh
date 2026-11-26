// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package indexing

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/convert"
)

func TestChunkText(t *testing.T) {
	text := make([]byte, 5000)
	for i := range text {
		text[i] = 'a'
	}

	chunks := ChunkText(string(text), 2000, 500)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}

	expected := []struct{ start, end int }{
		{0, 2000},
		{1500, 3500},
		{3000, 5000},
	}
	for i, exp := range expected {
		if chunks[i].Start != exp.start || chunks[i].End != exp.end {
			t.Errorf("chunk %d: got [%d, %d), want [%d, %d)",
				i, chunks[i].Start, chunks[i].End, exp.start, exp.end)
		}
		if len(chunks[i].Text) != exp.end-exp.start {
			t.Errorf("chunk %d: text length %d, want %d", i, len(chunks[i].Text), exp.end-exp.start)
		}
	}
}

func TestChunkTextSmall(t *testing.T) {
	chunks := ChunkText("hello world", 2000, 500)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Text != "hello world" {
		t.Errorf("chunk text mismatch")
	}
}

func TestChunkTextEmpty(t *testing.T) {
	chunks := ChunkText("", 2000, 500)
	if chunks != nil {
		t.Errorf("expected nil for empty text, got %v", chunks)
	}
}

func TestChunkTextWhitespaceOnly(t *testing.T) {
	chunks := ChunkText("   \n\t  ", 2000, 500)
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for whitespace-only, got %d", len(chunks))
	}
}

func TestBuildAndSerializeRoundTrip(t *testing.T) {
	builder := NewIndexBuilder(2000, 500)

	builder.AddDocument(Document{
		Filename: "doc1.txt",
		Version:  1,
		Conversion: &convert.Result{
			Text:         "The quick brown fox jumps over the lazy dog. Machine learning is great.",
			CitationType: "line_range",
			Citations: []convert.CitationRange{
				{Location: "lines_1_1", CharStart: 0, CharEnd: 70},
			},
		},
	})

	builder.AddDocument(Document{
		Filename:          "report.pdf.converted.txt",
		SourceFile:        "report.pdf",
		Version:           1,
		SourceFileVersion: 1,
		Conversion: &convert.Result{
			Text:         "Neural networks deep learning artificial intelligence research paper.",
			CitationType: "page",
			Citations: []convert.CitationRange{
				{Location: "physical_page_1", CharStart: 0, CharEnd: 69},
			},
		},
	})

	built, err := builder.Build("test-project")
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if built.Manifest.SchemaVersion != "1.0" {
		t.Errorf("schema version: got %q", built.Manifest.SchemaVersion)
	}
	if built.Manifest.FileCount != 2 {
		t.Errorf("file count: got %d, want 2", built.Manifest.FileCount)
	}
	if built.Manifest.ChunkCount != 2 {
		t.Errorf("chunk count: got %d, want 2", built.Manifest.ChunkCount)
	}

	c0 := built.Manifest.Chunks[0]
	if c0.Filename != "doc1.txt" || c0.DocID != 0 || c0.ChunkID != 0 {
		t.Errorf("chunk 0 metadata: %+v", c0)
	}
	if c0.CitationType != "line_range" {
		t.Errorf("chunk 0 citation_type: got %q", c0.CitationType)
	}

	c1 := built.Manifest.Chunks[1]
	if c1.SourceFile != "report.pdf" {
		t.Errorf("chunk 1 source_file: got %q", c1.SourceFile)
	}
	if c1.CitationType != "page" {
		t.Errorf("chunk 1 citation_type: got %q", c1.CitationType)
	}

	results := built.Index.Search([]string{"neural", "networks"}, 5)
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	if results[0].DocIndex != 1 {
		t.Errorf("expected doc 1 to rank first, got %d", results[0].DocIndex)
	}

	zipData, err := SerializeToZIP(built)
	if err != nil {
		t.Fatalf("SerializeToZIP failed: %v", err)
	}

	loaded, err := DeserializeFromZIP(zipData)
	if err != nil {
		t.Fatalf("DeserializeFromZIP failed: %v", err)
	}

	if loaded.Manifest.ProjectID != "test-project" {
		t.Errorf("project_id: got %q", loaded.Manifest.ProjectID)
	}
	if loaded.Manifest.ChunkCount != 2 {
		t.Errorf("chunk count after round-trip: got %d", loaded.Manifest.ChunkCount)
	}

	results2 := loaded.Index.Search([]string{"neural", "networks"}, 5)
	if len(results2) != len(results) {
		t.Fatalf("result count mismatch: %d vs %d", len(results2), len(results))
	}
	if results2[0].DocIndex != results[0].DocIndex {
		t.Errorf("rank 0 mismatch after round-trip")
	}
}

func TestOverlapCitations(t *testing.T) {
	citations := []convert.CitationRange{
		{Location: "physical_page_1", CharStart: 0, CharEnd: 1000},
		{Location: "physical_page_2", CharStart: 1000, CharEnd: 2500},
		{Location: "physical_page_3", CharStart: 2500, CharEnd: 4000},
	}

	result := overlapCitations(citations, 1500, 3500)
	if len(result) != 2 {
		t.Fatalf("expected 2 overlapping citations, got %d", len(result))
	}
	if result[0].Location != "physical_page_2" {
		t.Errorf("first overlap: got %q", result[0].Location)
	}
	if result[1].Location != "physical_page_3" {
		t.Errorf("second overlap: got %q", result[1].Location)
	}

	result = overlapCitations(citations, 0, 500)
	if len(result) != 1 || result[0].Location != "physical_page_1" {
		t.Errorf("unexpected result: %+v", result)
	}

	result = overlapCitations(citations, 4000, 5000)
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

func TestEndToEndConvertChunkIndexSearch(t *testing.T) {
	builder := NewIndexBuilder(ChunkSizeChars, OverlapChars)

	archText := "Solace Agent Mesh is a distributed AI agent framework enabling multi-agent " +
		"collaboration through event-driven messaging. The platform supports autonomous agents " +
		"that communicate via a shared message broker, enabling complex task decomposition " +
		"and parallel execution. Each agent specializes in specific domains like code generation, " +
		"data analysis, or knowledge retrieval. The BM25 full-text search engine provides " +
		"efficient document retrieval with citation tracking."
	builder.AddDocument(Document{
		Filename:   "architecture.pdf.converted.txt",
		SourceFile: "architecture.pdf",
		Version:    1,
		Conversion: &convert.Result{
			Text:         archText,
			CitationType: "page",
			Citations: []convert.CitationRange{
				{Location: "physical_page_1", CharStart: 0, CharEnd: len(archText)},
			},
		},
	})

	finText := "Q4 2025 Financial Report\n\n" +
		"Total revenue for Q4 2025 was $4.2 billion, representing a 15% year-over-year increase. " +
		"Operating expenses totaled $3.1 billion. Net income reached $800 million. " +
		"AI and machine learning initiatives contributed $500 million in new revenue streams. " +
		"The Asia-Pacific region showed the strongest growth at 25% YoY."
	builder.AddDocument(Document{
		Filename:   "financial_report.pdf.converted.txt",
		SourceFile: "financial_report.pdf",
		Version:    1,
		Conversion: &convert.Result{
			Text:         finText,
			CitationType: "page",
			Citations: []convert.CitationRange{
				{Location: "physical_page_1", CharStart: 0, CharEnd: len(finText)},
			},
		},
	})

	built, err := builder.Build("test-e2e")
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if built.Manifest.FileCount != 2 {
		t.Errorf("file count: got %d, want 2", built.Manifest.FileCount)
	}
	if built.Manifest.ChunkCount < 2 {
		t.Errorf("expected at least 2 chunks, got %d", built.Manifest.ChunkCount)
	}

	zipData, err := SerializeToZIP(built)
	if err != nil {
		t.Fatalf("SerializeToZIP failed: %v", err)
	}
	loaded, err := DeserializeFromZIP(zipData)
	if err != nil {
		t.Fatalf("DeserializeFromZIP failed: %v", err)
	}

	results := loaded.Index.Search([]string{"revenue"}, 5)
	if len(results) == 0 {
		t.Fatal("expected results for 'revenue'")
	}
	topChunk := loaded.Manifest.Chunks[results[0].DocIndex]
	if topChunk.SourceFile != "financial_report.pdf" {
		t.Errorf("expected financial_report.pdf to rank first, got source_file=%q", topChunk.SourceFile)
	}

	results = loaded.Index.Search([]string{"agent", "framework"}, 5)
	if len(results) == 0 {
		t.Fatal("expected results for 'agent framework'")
	}
	topChunk = loaded.Manifest.Chunks[results[0].DocIndex]
	if topChunk.SourceFile != "architecture.pdf" {
		t.Errorf("expected architecture.pdf to rank first, got source_file=%q", topChunk.SourceFile)
	}

	results = loaded.Index.Search([]string{"ai"}, 10)
	if len(results) < 2 {
		t.Errorf("expected at least 2 results for 'ai', got %d", len(results))
	}

	for i, chunk := range loaded.Manifest.Chunks {
		if chunk.CitationType == "" {
			t.Errorf("chunk %d missing citation_type", i)
		}
		if len(chunk.CitationMap) == 0 {
			t.Errorf("chunk %d has empty citation_map", i)
		}
		if chunk.ChunkText == "" {
			t.Errorf("chunk %d has empty chunk_text", i)
		}
	}

	for _, chunk := range loaded.Manifest.Chunks {
		if chunk.SourceFile == "" {
			t.Errorf("chunk for %q missing source_file", chunk.Filename)
		}
		if strings.Contains(chunk.SourceFile, ".converted.txt") {
			t.Errorf("source_file should be original filename, got %q", chunk.SourceFile)
		}
	}
}

func TestEndToEndWithRealConversion(t *testing.T) {
	builder := NewIndexBuilder(ChunkSizeChars, OverlapChars)

	docxResult, err := convert.Convert("report.docx", buildTestDOCX(t), "")
	if err != nil {
		t.Fatalf("Convert DOCX failed: %v", err)
	}
	builder.AddDocument(Document{
		Filename:   "report.docx.converted.txt",
		SourceFile: "report.docx",
		Version:    1,
		Conversion: docxResult,
	})

	pptxResult, err := convert.Convert("slides.pptx", buildTestPPTX(t), "")
	if err != nil {
		t.Fatalf("Convert PPTX failed: %v", err)
	}
	builder.AddDocument(Document{
		Filename:   "slides.pptx.converted.txt",
		SourceFile: "slides.pptx",
		Version:    1,
		Conversion: pptxResult,
	})

	textContent := "Machine learning algorithms process data to build predictive models.\n" +
		"Deep neural networks enable computer vision and natural language processing.\n" +
		"Reinforcement learning trains agents through reward signals."
	textResult, err := convert.Convert("notes.txt", []byte(textContent), "")
	if err != nil {
		t.Fatalf("Convert text failed: %v", err)
	}
	builder.AddDocument(Document{
		Filename:   "notes.txt",
		SourceFile: "",
		Version:    1,
		Conversion: textResult,
	})

	built, err := builder.Build("real-conv-test")
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	zipData, err := SerializeToZIP(built)
	if err != nil {
		t.Fatalf("SerializeToZIP failed: %v", err)
	}
	loaded, err := DeserializeFromZIP(zipData)
	if err != nil {
		t.Fatalf("DeserializeFromZIP failed: %v", err)
	}

	citationTypes := map[string]bool{}
	for _, chunk := range loaded.Manifest.Chunks {
		citationTypes[chunk.CitationType] = true
	}
	if !citationTypes["paragraph"] {
		t.Error("expected 'paragraph' citation type from DOCX")
	}
	if !citationTypes["slide"] {
		t.Error("expected 'slide' citation type from PPTX")
	}
	if !citationTypes["line_range"] {
		t.Error("expected 'line_range' citation type from text")
	}

	results := loaded.Index.Search([]string{"machine", "learning"}, 5)
	if len(results) == 0 {
		t.Fatal("expected results for 'machine learning'")
	}

	results = loaded.Index.Search([]string{"quarterly", "review"}, 5)
	if len(results) == 0 {
		t.Fatal("expected results for 'quarterly review' (PPTX content)")
	}
}

func buildTestDOCX(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	xmlContent := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:body>` +
		`<w:p><w:r><w:t>Executive Summary: AI Strategy Report</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>Our organization is investing heavily in machine learning and artificial intelligence.</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>Key initiatives include natural language processing and computer vision systems.</w:t></w:r></w:p>` +
		`</w:body></w:document>`

	f, _ := w.Create("word/document.xml")
	_, _ = f.Write([]byte(xmlContent))
	_ = w.Close()
	return buf.Bytes()
}

func buildTestPPTX(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	slide1 := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
		`<p:cSld><p:spTree>` +
		`<p:sp><p:txBody><a:p><a:r><a:t>Quarterly Business Review</a:t></a:r></a:p></p:txBody></p:sp>` +
		`<p:sp><p:txBody><a:p><a:r><a:t>Q4 2025 Performance Analysis</a:t></a:r></a:p></p:txBody></p:sp>` +
		`</p:spTree></p:cSld></p:sld>`

	slide2 := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
		`<p:cSld><p:spTree>` +
		`<p:sp><p:txBody><a:p><a:r><a:t>Revenue grew 15% year over year driven by cloud services adoption</a:t></a:r></a:p></p:txBody></p:sp>` +
		`</p:spTree></p:cSld></p:sld>`

	f1, _ := w.Create("ppt/slides/slide1.xml")
	_, _ = f1.Write([]byte(slide1))
	f2, _ := w.Create("ppt/slides/slide2.xml")
	_, _ = f2.Write([]byte(slide2))
	_ = w.Close()
	return buf.Bytes()
}
