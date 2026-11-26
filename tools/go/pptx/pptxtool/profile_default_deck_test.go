// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const deckSpec = `
- layout: title
  kicker: "Quarterly Review"
  title: "Revenue grew"
- layout: section
  kicker: "Part Two"
  title: "Regions"
- layout: big_number
  kicker: "Headline"
  title: "Net revenue"
  big_number: "$32.2M"
  big_label: "FY26"
- layout: bullet_list
  title: "Findings"
  bullets: ["One", "Two"]
- layout: closing
  title: "Thank you"
`

func runTool(t *testing.T, config map[string]any, spec string) *sdk.Result {
	t.Helper()
	tctx := &sdk.ToolContext{}
	tctx.SetConfigForTest(config)
	out := "deck"
	res, err := createPresentation(t.Context(), CreatePresentationParams{
		Title:      "Demo",
		OutputName: &out,
		Slides:     sdk.Artifact{Filename: "spec.yaml", Content: []byte(spec), MIMEType: "application/yaml"},
	}, tctx)
	if err != nil {
		t.Fatalf("createPresentation: %v", err)
	}
	return res
}

func unzipDeck(t *testing.T, res *sdk.Result) map[string][]byte {
	t.Helper()
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, want success: %s", res.Status, res.Message)
	}
	archive := res.DataObjects[0].Content
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("open deck: %v", err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, openErr := f.Open()
		if openErr != nil {
			t.Fatalf("open %s: %v", f.Name, openErr)
		}
		data, readErr := io.ReadAll(rc)
		if closeErr := rc.Close(); closeErr != nil {
			t.Fatalf("close %s: %v", f.Name, closeErr)
		}
		if readErr != nil {
			t.Fatalf("read %s: %v", f.Name, readErr)
		}
		files[f.Name] = data
	}
	return files
}

func fileNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func TestCreatePresentation_UnknownBuiltinProfileIsReported(t *testing.T) {
	for _, name := range []string{"solace2025", "solace-2025", "corporate"} {
		t.Run(name, func(t *testing.T) {
			res := runTool(t, map[string]any{"template_profile": name}, deckSpec)
			if res.Status == sdk.StatusSuccess {
				t.Fatalf("a deck was built with template_profile %q, which names no profile: it renders as plain slides, the unstyled look an operator never chose", name)
			}
			for _, want := range []string{name, "neutral", "solace_2025", "none"} {
				if !strings.Contains(res.Message, want) {
					t.Errorf("the error %q does not mention %q", res.Message, want)
				}
			}
		})
	}
}

func TestCreatePresentation_UnloadableProfilePathIsReported(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"missing file": filepath.Join(dir, "brand.json"),
		"invalid json": broken,
	} {
		t.Run(name, func(t *testing.T) {
			res := runTool(t, map[string]any{"template_profile_path": path}, deckSpec)
			if res.Status == sdk.StatusSuccess {
				t.Fatalf("a deck was built with template_profile_path %q, which does not load: it renders as plain slides, the unstyled look an operator never chose", path)
			}
			if !strings.Contains(res.Message, path) {
				t.Errorf("the error %q does not name the path %q", res.Message, path)
			}
		})
	}

	valid, err := profilesFS.ReadFile("profiles/solace_2025.json")
	if err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	fromPath := fileNames(unzipDeck(t, runTool(t, map[string]any{"template_profile_path": good}, deckSpec)))
	byName := fileNames(unzipDeck(t, runTool(t, map[string]any{"template_profile": "solace_2025"}, deckSpec)))
	if !slices.Equal(fromPath, byName) {
		t.Errorf("a path to the solace_2025 profile built a different package than the name:\n got %v\nwant %v", fromPath, byName)
	}
}

func TestCreatePresentation_ProfileNameIgnoresCase(t *testing.T) {
	want := fileNames(unzipDeck(t, runTool(t, map[string]any{"template_profile": "solace_2025"}, deckSpec)))
	for _, name := range []string{"Solace_2025", " SOLACE_2025 "} {
		got := fileNames(unzipDeck(t, runTool(t, map[string]any{"template_profile": name}, deckSpec)))
		if !slices.Equal(got, want) {
			t.Errorf("template_profile %q built a different package than solace_2025:\n got %v\nwant %v", name, got, want)
		}
	}
}

func TestCreatePresentation_DefaultDeckHasNoSolaceAnywhere(t *testing.T) {
	textHits := func(files map[string][]byte) []string {
		var found []string
		for name, data := range files {
			if strings.Contains(strings.ToLower(name), "solace") || bytes.Contains(bytes.ToLower(data), []byte("solace")) {
				found = append(found, name)
			}
		}
		slices.Sort(found)
		return found
	}
	solace, err := loadBuiltinProfile("solace_2025")
	if err != nil {
		t.Fatalf("load solace_2025: %v", err)
	}
	brandImages := map[string]bool{}
	for _, a := range solace.Assets {
		brandImages[string(a.Data)] = true
	}
	imageHits := func(files map[string][]byte) int {
		n := 0
		for name, data := range files {
			if strings.HasPrefix(name, "ppt/media/") && brandImages[string(data)] {
				n++
			}
		}
		return n
	}

	def := unzipDeck(t, runTool(t, nil, deckSpec))
	if got := textHits(def); len(got) > 0 {
		t.Errorf("the default deck mentions Solace in %v", got)
	}
	if got := imageHits(def); got > 0 {
		t.Errorf("the default deck embeds %d of the Solace profile's images", got)
	}

	planted := unzipDeck(t, runTool(t, nil, strings.Replace(deckSpec, "Revenue grew", "Solace revenue grew", 1)))
	if len(textHits(planted)) == 0 {
		t.Error("a deck titled \"Solace\" has no match, so the text search cannot detect a leak")
	}
	if imageHits(unzipDeck(t, runTool(t, map[string]any{"template_profile": "solace_2025"}, deckSpec))) == 0 {
		t.Error("the solace_2025 deck embeds none of the profile's images, so the image search cannot detect a leak")
	}
}

var (
	kickerRunColor = regexp.MustCompile(`srgbClr val="([0-9A-Fa-f]{6})"`)
	slideBgColor   = regexp.MustCompile(`(?s)<p:bg>.*?srgbClr val="([0-9A-Fa-f]{6})"`)
)

func kickerContrast(t *testing.T, files map[string][]byte, slide int, label string) float64 {
	t.Helper()
	xml := string(files["ppt/slides/slide"+strconv.Itoa(slide)+".xml"])
	bg := slideBgColor.FindStringSubmatch(xml)
	if bg == nil {
		t.Fatalf("slide %d has no background colour", slide)
	}
	for run := range strings.SplitSeq(xml, "<a:r>") {
		if !strings.Contains(run, ">"+label+"<") && !strings.Contains(run, ">"+strings.ToUpper(label)+"<") {
			continue
		}
		fg := kickerRunColor.FindStringSubmatch(run)
		if fg == nil {
			t.Fatalf("the %q run on slide %d has no colour", label, slide)
		}
		return contrastRatio(fg[1], bg[1])
	}
	t.Fatalf("slide %d has no run reading %q", slide, label)
	return 0
}

func TestNeutralDeck_KickersAreReadableOnDarkSlides(t *testing.T) {
	files := unzipDeck(t, runTool(t, nil, deckSpec))
	for _, tc := range []struct {
		slide int
		label string
	}{{1, "Quarterly Review"}, {2, "Part Two"}, {3, "Headline"}} {
		if got := kickerContrast(t, files, tc.slide, tc.label); got < 4.5 {
			t.Errorf("slide %d kicker %q has contrast %.2f:1 on its background, below 4.5:1", tc.slide, tc.label, got)
		}
	}
}

func TestSolaceDeck_KickerColourIsUnchanged(t *testing.T) {
	files := unzipDeck(t, runTool(t, map[string]any{"template_profile": "solace_2025"}, deckSpec))
	xml := string(files["ppt/slides/slide1.xml"])
	for run := range strings.SplitSeq(xml, "<a:r>") {
		if strings.Contains(strings.ToUpper(run), ">QUARTERLY REVIEW<") {
			if got := kickerRunColor.FindStringSubmatch(run); len(got) < 2 || !strings.EqualFold(got[1], "00C895") {
				t.Errorf("the solace_2025 title kicker colour = %v, want 00C895", got)
			}
			return
		}
	}
	t.Fatal("no kicker run found on the solace_2025 title slide")
}

var offY = regexp.MustCompile(`<a:off x="-?\d+" y="(\d+)"`)

func titleY(t *testing.T, files map[string][]byte, slide int, text string) int {
	t.Helper()
	xml := string(files["ppt/slides/slide"+strconv.Itoa(slide)+".xml"])
	for shape := range strings.SplitSeq(xml, "<p:sp>") {
		if !strings.Contains(shape, ">"+text+"<") {
			continue
		}
		m := offY.FindStringSubmatch(shape)
		if m == nil {
			t.Fatalf("the shape reading %q has no offset", text)
		}
		y, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("offset %q: %v", m[1], err)
		}
		return y
	}
	t.Fatalf("slide %d has no shape reading %q", slide, text)
	return 0
}

func TestClosingTitle_MovesDownOnlyToClearALogo(t *testing.T) {
	neutral := titleY(t, unzipDeck(t, runTool(t, nil, deckSpec)), 5, "Thank you")
	solace := titleY(t, unzipDeck(t, runTool(t, map[string]any{"template_profile": "solace_2025"}, deckSpec)), 5, "Thank you")
	if neutral >= solace {
		t.Errorf("closing title y: neutral = %d, solace_2025 = %d; neutral must sit above the solace title, which clears a logo", neutral, solace)
	}
}
