// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata golden files")

var fixedTimeForGolden = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

const goldenDir = "testdata/empty_deck"

func buildEmptyDeck() *Presentation {
	p := New()
	p.SetTitle("pptxgen empty deck")
	p.SetAuthor("pptxgen tests")
	p.setFixedTime(fixedTimeForGolden)
	_ = p.AddSlide()
	s2 := p.AddSlide()
	s2.SetBackground(BackgroundOptions{Color: "#D6E4F0"})
	return p
}

func TestEmptyDeck(t *testing.T) {
	p := buildEmptyDeck()
	out, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}

	got := map[string][]byte{}
	for _, f := range zr.File {
		rc, oerr := f.Open()
		if oerr != nil {
			t.Fatalf("open %q: %v", f.Name, oerr)
		}
		data, rerr := io.ReadAll(rc)
		_ = rc.Close()
		if rerr != nil {
			t.Fatalf("read %q: %v", f.Name, rerr)
		}
		got[f.Name] = data
	}

	for name, data := range got {
		if perr := parseAll(data); perr != nil {
			t.Errorf("malformed XML in %s: %v", name, perr)
		}
	}

	if *updateGolden {
		if werr := writeGoldens(got); werr != nil {
			t.Fatalf("write goldens: %v", werr)
		}
		t.Logf("wrote %d golden files to %s", len(got), goldenDir)
		return
	}

	want, err := readGoldens()
	if err != nil {
		t.Fatalf("read goldens: %v", err)
	}
	for _, name := range sortedKeys(want) {
		w := want[name]
		g, ok := got[name]
		if !ok {
			t.Errorf("missing emitted part: %s", name)
			continue
		}
		if !bytes.Equal(w, g) {
			t.Errorf("part %s differs from golden\n--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s",
				name, len(w), preview(w), len(g), preview(g))
		}
	}
	for _, name := range sortedKeys(got) {
		if _, ok := want[name]; !ok {
			t.Errorf("emitted part not in goldens: %s", name)
		}
	}
}

func TestEmptyDeckZipIsClean(t *testing.T) {
	p := buildEmptyDeck()
	out, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	for _, f := range zr.File {
		rc, oerr := f.Open()
		if oerr != nil {
			t.Fatalf("open %q: %v", f.Name, oerr)
		}
		_, cerr := io.Copy(io.Discard, rc)
		_ = rc.Close()
		if cerr != nil {
			t.Fatalf("read %q: %v", f.Name, cerr)
		}
	}
}

func TestCorePropsDescriptionAndKeywords(t *testing.T) {
	readCore := func(p *Presentation) string {
		out, err := p.ToBytes()
		if err != nil {
			t.Fatalf("ToBytes: %v", err)
		}
		zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
		if err != nil {
			t.Fatalf("zip.NewReader: %v", err)
		}
		for _, f := range zr.File {
			if f.Name != "docProps/core.xml" {
				continue
			}
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			_ = rc.Close()
			return string(b)
		}
		t.Fatal("docProps/core.xml not found")
		return ""
	}

	p := New()
	p.SetTitle("t")
	p.SetDescription("Icon glyphs derived from Bootstrap Icons (MIT).")
	p.SetKeywords("Bootstrap Icons; MIT")
	_ = p.AddSlide()
	core := readCore(p)
	if !bytes.Contains([]byte(core), []byte("<dc:description>Icon glyphs derived from Bootstrap Icons (MIT).</dc:description>")) {
		t.Errorf("dc:description missing:\n%s", core)
	}
	if !bytes.Contains([]byte(core), []byte("<cp:keywords>Bootstrap Icons; MIT</cp:keywords>")) {
		t.Errorf("cp:keywords missing:\n%s", core)
	}

	bare := New()
	_ = bare.AddSlide()
	if got := readCore(bare); bytes.Contains([]byte(got), []byte("dc:description")) {
		t.Errorf("dc:description should be omitted when empty:\n%s", got)
	}
}

func TestDeterministicOutput(t *testing.T) {
	a, err := buildEmptyDeck().ToBytes()
	if err != nil {
		t.Fatalf("ToBytes #1: %v", err)
	}
	b, err := buildEmptyDeck().ToBytes()
	if err != nil {
		t.Fatalf("ToBytes #2: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("two ToBytes() calls produced different bytes: %d vs %d", len(a), len(b))
	}
}

func TestSlideBackgroundEmpty(t *testing.T) {
	p := New()
	p.setFixedTime(fixedTimeForGolden)
	s := p.AddSlide()
	s.SetBackground(BackgroundOptions{Color: "#FF0000"})
	s.SetBackground(BackgroundOptions{Color: ""})
	out, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	xmlBytes := findEntry(t, out, "ppt/slides/slide1.xml")
	if bytes.Contains(xmlBytes, []byte("<p:bg>")) {
		t.Errorf("slide background was cleared but <p:bg> still present:\n%s", xmlBytes)
	}
}

func TestSlideBackgroundUnparseable(t *testing.T) {
	for _, in := range []string{"", "   ", "not a hex", "#1234", "##abcdef"} {
		p := New()
		p.setFixedTime(fixedTimeForGolden)
		s := p.AddSlide()
		s.SetBackground(BackgroundOptions{Color: in})
		if s.bg != nil {
			t.Errorf("SetBackground(%q): expected nil bg, got %+v", in, s.bg)
		}
	}
}

func TestEscapedTitlePropagates(t *testing.T) {
	p := New()
	p.setFixedTime(fixedTimeForGolden)
	p.SetTitle("Q1 <Strategy & Plan> \x00")
	p.SetAuthor("alice")
	p.AddSlide()
	out, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	core := findEntry(t, out, "docProps/core.xml")
	want := []byte("Q1 &lt;Strategy &amp; Plan&gt; ")
	if !bytes.Contains(core, want) {
		t.Errorf("escaped title not present in core.xml.\nlooking for: %q\ngot: %s", want, core)
	}
	if bytes.Contains(core, []byte{0x00}) {
		t.Errorf("NUL byte survived into core.xml: %q", core)
	}
}

func TestZeroSlideErrors(t *testing.T) {
	if _, err := New().ToBytes(); err == nil {
		t.Fatalf("ToBytes on zero-slide presentation: want error, got nil")
	}
}

func parseAll(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func findEntry(t *testing.T, out []byte, name string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, oerr := f.Open()
			if oerr != nil {
				t.Fatalf("open %q: %v", name, oerr)
			}
			data, rerr := io.ReadAll(rc)
			_ = rc.Close()
			if rerr != nil {
				t.Fatalf("read %q: %v", name, rerr)
			}
			return data
		}
	}
	t.Fatalf("entry %q not found in archive", name)
	return nil
}

func readGoldens() (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(goldenDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(goldenDir, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[key] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func writeGoldens(parts map[string][]byte) error {
	if err := os.RemoveAll(goldenDir); err != nil {
		return err
	}
	for name, data := range parts {
		out := filepath.Join(goldenDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func preview(b []byte) string {
	const max = 400
	if len(b) > max {
		b = b[:max]
	}
	return fmt.Sprintf("%q", b)
}
