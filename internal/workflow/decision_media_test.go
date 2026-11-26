// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"

	"golang.org/x/image/bmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/visionimage"
)

const geminiBlock = "decision:\n  provider: google_ai_studio\n  api_key: test-key\n  model: gemini-decision-1\n"

func geminiMediaWorkflow(nodeExtra, decisionExtra string) string {
	doc := strings.Replace(routedDecisionWorkflow, inlineTypeSafeBlock, geminiBlock+decisionExtra, 1)
	return strings.Replace(doc, "    route_on: department\n", nodeExtra+"    route_on: department\n", 1)
}

func mediaFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "decision", "testdata", "media", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func withDecisionMediaInput(t *testing.T) func(string, *stubServices) {
	t.Helper()
	t.Setenv(features.EnvVarName(features.KeyDecisionMediaInput), "")
	return func(_ string, svc *stubServices) {
		svc.features.Register(features.Definition{Key: features.KeyDecisionMediaInput, Default: true})
	}
}

func (f *fakeJev) geminiMediaBlocks(t *testing.T) []map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	input, _ := f.lastBody["input"].([]any)
	var blocks []map[string]any
	for _, raw := range input {
		if block, _ := raw.(map[string]any); block["type"] != "text" {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

func TestDecisionNodeSendsEveryMediaKindToGemini(t *testing.T) {
	heic := mediaFixture(t, "image.heic")
	files := map[string][]byte{
		"photo.heic":  heic,
		"invoice.pdf": mediaFixture(t, "document.pdf"),
		"call.wav":    mediaFixture(t, "tone.wav"),
		"clip.mp4":    mediaFixture(t, "clip.mp4"),
	}
	doc := geminiMediaWorkflow(
		"    images: [photo.heic]\n    image_resolution: high\n    documents: [invoice.pdf]\n    audio: [call.wav]\n    video: [clip.mp4]\n    video_resolution: low\n",
		"  max_media: 3\n")
	jev := &fakeJev{respond: geminiDepartmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, doc, jev, true, withArtifacts(t, files), withDecisionMediaInput(t))
	run.execute(t)

	run.assertBranches(t, "billing", "tech", "fallback")
	type sent struct{ kind, mime, resolution string }
	want := []sent{
		{"image", "image/heic", "high"},
		{"document", "application/pdf", ""},
		{"audio", "audio/wav", ""},
		{"video", "video/mp4", "low"},
	}
	blocks := jev.geminiMediaBlocks(t)
	if len(blocks) != len(want) {
		t.Fatalf("sent %d media blocks, want %d: %v", len(blocks), len(want), blocks)
	}
	for i, w := range want {
		res, _ := blocks[i]["resolution"].(string)
		if blocks[i]["type"] != w.kind || blocks[i]["mime_type"] != w.mime || res != w.resolution {
			t.Errorf("block %d = %v %v %q, want %+v", i, blocks[i]["type"], blocks[i]["mime_type"], res, w)
		}
	}
	if blocks[0]["data"] != base64.StdEncoding.EncodeToString(heic) {
		t.Error("the HEIC image was changed; it must go unchanged")
	}

	out := run.output(t, "triage")
	for key, name := range map[string]string{"images": "photo.heic", "documents": "invoice.pdf", "audio": "call.wav", "video": "clip.mp4"} {
		list, _ := out[key].([]any)
		if len(list) != 1 {
			t.Errorf("output %s = %v, want one entry", key, out[key])
			continue
		}
		if entry, _ := list[0].(map[string]any); entry["name"] != name || entry["bytes"] != len(files[name]) {
			t.Errorf("output %s[0] = %v, want %s at %d bytes", key, entry, name, len(files[name]))
		}
	}
}

func TestDecisionNodeImageOnlyOutputKeepsItsShape(t *testing.T) {
	photo := pngImage(t, 4, 4)
	jev := &fakeJev{respond: geminiDepartmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, geminiMediaWorkflow("    images: [photo.png]\n", ""), jev, true,
		withArtifacts(t, map[string][]byte{"photo.png": photo}), withDecisionMediaInput(t))
	run.execute(t)

	out := run.output(t, "triage")
	for _, key := range []string{"documents", "audio", "video"} {
		if _, ok := out[key]; ok {
			t.Errorf("an image-only node's output has %s: %v", key, out[key])
		}
	}
	images, _ := out["images"].([]any)
	if len(images) != 1 {
		t.Fatalf("output images = %v, want one entry", out["images"])
	}
	assertSentImageEntry(t, images[0], "photo.png", len(photo), nil)
}

func TestDecisionMediaNeedsItsFeatureFlag(t *testing.T) {
	for key, extra := range map[string]string{
		"audio":            "    audio: [call.wav]\n",
		"documents":        "    documents: [a.pdf]\n",
		"video":            "    video: [clip.mp4]\n",
		"image_resolution": "    images: [a.png]\n    image_resolution: low\n",
		"video_resolution": "    video_resolution: low\n",
	} {
		t.Run(key, func(t *testing.T) {
			jev := &fakeJev{respond: geminiDepartmentAnswer("billing", 0.9)}
			run := newDecisionRun(t, geminiMediaWorkflow(extra, ""), jev, true,
				withArtifacts(t, map[string][]byte{"call.wav": mediaFixture(t, "tone.wav"), "a.pdf": mediaFixture(t, "document.pdf"), "a.png": pngImage(t, 4, 4), "clip.mp4": mediaFixture(t, "clip.mp4")}))
			run.execute(t)

			run.assertNoProviderCall(t)
			msg := run.errorMessage()
			if !strings.Contains(msg, "sets "+key+",") || !strings.Contains(msg, "SAM_FEATURE_DECISION_MEDIA_INPUT=true") {
				t.Errorf("workflow error = %q, want the node failed naming %s and the flag", msg, key)
			}
		})
	}
	t.Run("on_provider_error escalate does not route it", func(t *testing.T) {
		jev := &fakeJev{respond: geminiDepartmentAnswer("billing", 0.9)}
		doc := strings.Replace(geminiMediaWorkflow("    audio: [call.wav]\n", ""), "    default: fallback\n", escalateOnProviderError, 1)
		run := newDecisionRun(t, doc, jev, true,
			withArtifacts(t, map[string][]byte{"call.wav": mediaFixture(t, "tone.wav")}))
		run.execute(t)

		run.assertNoProviderCall(t)
		if msg := run.errorMessage(); !strings.Contains(msg, "SAM_FEATURE_DECISION_MEDIA_INPUT=true") {
			t.Errorf("workflow error = %q, want the node failed naming the flag rather than escalated", msg)
		}
	})
	t.Run("an image-only node runs with the flag off", func(t *testing.T) {
		jev := &fakeJev{respond: geminiDepartmentAnswer("billing", 0.9)}
		run := newDecisionRun(t, geminiMediaWorkflow("    images: [a.png]\n", ""), jev, true,
			withArtifacts(t, map[string][]byte{"a.png": pngImage(t, 4, 4)}))
		run.execute(t)
		run.assertBranches(t, "billing", "tech", "fallback")
	})
}

func TestCheckDecisionMediaSupported(t *testing.T) {
	openAI := "decision:\n  provider: openai\n  api_key: test-key\n  model: gpt-6-luna\n"
	alias := "decision:\n  provider: openai\n  model_alias: decider\n"
	cases := map[string]struct {
		block, node, wantErr string
	}{
		"audio on OpenAI":                 {openAI, "    audio: [call.wav]\n", `sends audio, but the openai decision model "gpt-6-luna" does not read it; use a google_ai_studio decision model`},
		"a document on OpenAI":            {openAI, "    documents: [a.pdf]\n", "sends documents"},
		"image_resolution on Jev":         {inlineTypeSafeBlock, "    images: [a.png]\n    image_resolution: low\n", "sets image_resolution, which only a google_ai_studio decision model reads"},
		"video_resolution on Jev":         {inlineTypeSafeBlock, "    video_resolution: low\n", "sets video_resolution"},
		"too many literal files":          {geminiBlock, "    audio: [a.wav, b.wav]\n    video: [c.mp4]\n", "names 3 audio, video and document files, but the decision block's max_media is 2"},
		"templates are not counted":       {geminiBlock, "    audio: [a.wav, b.wav, '{{workflow.input.call}}']\n", ""},
		"audio on Gemini":                 {geminiBlock, "    audio: [call.wav]\n    video: [clip.mp4]\n    video_resolution: high\n", ""},
		"an alias is checked when called": {alias, "    audio: [call.wav]\n", ""},
		"images on OpenAI":                {openAI, "    images: [a.png]\n", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			doc := strings.Replace(routedDecisionWorkflow, inlineTypeSafeBlock, tc.block, 1)
			doc = strings.Replace(doc, "    route_on: department\n", tc.node+"    route_on: department\n", 1)
			cfg, err := parseYAMLWorkflow(t, doc)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			err = checkDecisionMediaSupported(cfg.Decision, cfg.Nodes)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v, want none", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestDecisionNodeFailsOnTheWrongKindOfMedia(t *testing.T) {
	jev := &fakeJev{respond: geminiDepartmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, geminiMediaWorkflow("    video: [song.mp3]\n", ""), jev, true,
		withArtifacts(t, map[string][]byte{"song.mp3": mediaFixture(t, "tone.mp3")}), withDecisionMediaInput(t))
	run.execute(t)

	run.assertNoProviderCall(t)
	if msg := run.errorMessage(); !strings.Contains(msg, `artifact "song.mp3" is audio/mpeg, not a video a decision accepts`) {
		t.Errorf("workflow error = %q, want the MP3 refused as a video", msg)
	}
}

func TestDecisionNodeEscalatesOverTheMediaCaps(t *testing.T) {
	wav := mediaFixture(t, "tone.wav")
	cases := map[string]struct {
		node, decisionExtra string
	}{
		"over max_media_bytes": {"    audio: [call.wav]\n", "  max_media_bytes: " + jsonNumber(float64(len(wav)-1)) + "\n"},
		"over max_media":       {"    audio: ['{{workflow.input.calls}}']\n", "  max_media: 1\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			doc := strings.Replace(geminiMediaWorkflow(tc.node, tc.decisionExtra), "    default: fallback\n", escalateOnProviderError, 1)
			jev := &fakeJev{respond: geminiDepartmentAnswer("billing", 0.9)}
			run := newDecisionRun(t, doc, jev, true,
				withArtifacts(t, map[string][]byte{"call.wav": wav, "b.wav": wav}), withDecisionMediaInput(t))
			withWorkflowInput(run, map[string]any{"calls": []any{"call.wav", "b.wav"}})
			run.execute(t)

			run.assertNoProviderCall(t)
			run.assertEscalatedOn(t, decision.FailureInputTooLarge)
		})
	}
}

func TestDecisionNodeImageTypesFollowTheFlag(t *testing.T) {
	var wide bytes.Buffer
	if err := bmp.Encode(&wide, image.NewRGBA(image.Rect(0, 0, visionimage.MaxDim+500, 4))); err != nil {
		t.Fatal(err)
	}
	gif := mediaFixture(t, "image.gif")
	fourBitTIFF := mediaFixture(t, "image_4bit.tiff")
	files := map[string][]byte{"a.gif": gif, "wide.bmp": wide.Bytes(), "scan.tiff": fourBitTIFF}

	t.Run("with the flag off a GIF fails the node", func(t *testing.T) {
		jev := &fakeJev{respond: geminiDepartmentAnswer("billing", 0.9)}
		run := newDecisionRun(t, geminiMediaWorkflow("    images: [a.gif]\n", ""), jev, true, withArtifacts(t, files))
		run.execute(t)
		run.assertNoProviderCall(t)
		if msg := run.errorMessage(); !strings.Contains(msg, "not an image a decision accepts (image/png, image/jpeg, image/webp)") {
			t.Errorf("workflow error = %q, want the GIF refused as before", msg)
		}
	})

	t.Run("with the flag on a BMP variant Go cannot decode goes to Gemini unchanged", func(t *testing.T) {
		var paletted bytes.Buffer
		if err := bmp.Encode(&paletted, image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{color.White, color.Black})); err != nil {
			t.Fatal(err)
		}
		const bmpCompressionOffset, bmpRLE8 = 30, 1
		rle := paletted.Bytes()
		rle[bmpCompressionOffset] = bmpRLE8
		jev := &fakeJev{respond: geminiDepartmentAnswer("billing", 0.9)}
		run := newDecisionRun(t, geminiMediaWorkflow("    images: [rle.bmp]\n", ""), jev, true,
			withArtifacts(t, map[string][]byte{"rle.bmp": rle}), withDecisionMediaInput(t))
		run.execute(t)
		run.assertBranches(t, "billing", "tech", "fallback")
		blocks := jev.geminiMediaBlocks(t)
		if len(blocks) != 1 || blocks[0]["mime_type"] != "image/bmp" || blocks[0]["data"] != base64.StdEncoding.EncodeToString(rle) {
			t.Errorf("sent %v, want the RLE BMP unchanged as image/bmp", blocks)
		}
	})

	t.Run("with the flag on Gemini gets each as it can read it", func(t *testing.T) {
		jev := &fakeJev{respond: geminiDepartmentAnswer("billing", 0.9)}
		run := newDecisionRun(t, geminiMediaWorkflow("    images: [a.gif, wide.bmp, scan.tiff]\n", ""), jev, true,
			withArtifacts(t, files), withDecisionMediaInput(t))
		run.execute(t)
		run.assertBranches(t, "billing", "tech", "fallback")

		blocks := jev.geminiMediaBlocks(t)
		if len(blocks) != 3 {
			t.Fatalf("sent %d images, want 3", len(blocks))
		}
		if blocks[0]["mime_type"] != "image/gif" || blocks[0]["data"] != base64.StdEncoding.EncodeToString(gif) {
			t.Errorf("the GIF went as %v, want it unchanged", blocks[0]["mime_type"])
		}
		if blocks[1]["mime_type"] != "image/png" {
			t.Errorf("the oversized BMP went as %v, want a downscaled PNG", blocks[1]["mime_type"])
		} else if data, err := base64.StdEncoding.DecodeString(blocks[1]["data"].(string)); err == nil {
			if cfg, err := png.DecodeConfig(bytes.NewReader(data)); err != nil || cfg.Width != visionimage.MaxDim {
				t.Errorf("the downscaled BMP is %dpx wide (%v), want %d", cfg.Width, err, visionimage.MaxDim)
			}
		}
		if blocks[2]["mime_type"] != "image/tiff" || blocks[2]["data"] != base64.StdEncoding.EncodeToString(fourBitTIFF) {
			t.Errorf("the TIFF the decoder cannot read went as %v, want it unchanged", blocks[2]["mime_type"])
		}
	})
}

func TestParseDecisionMediaKeys(t *testing.T) {
	cases := map[string]struct {
		node, decisionExtra, wantErr string
	}{
		"an image_resolution Google does not take": {"    image_resolution: huge\n", "", "image_resolution must be one of low, medium, high, ultra_high"},
		"ultra_high video":                         {"    video_resolution: ultra_high\n", "", "video_resolution must be one of low, medium, high"},
		"audio that is not a list":                 {"    audio: call.wav\n", "", "audio must be a list of artifact references"},
		"a blank video entry":                      {"    video: ['']\n", "", "video[0] must be a non-empty artifact reference"},
		"max_media over the ceiling":               {"", "  max_media: 129\n", "max_media must be between 0 (the default, 2) and 128"},
		"max_media_bytes over the ceiling":         {"", "  max_media_bytes: 67108865\n", "max_media_bytes must be between 0"},
		"camelCase keys":                           {"    imageResolution: medium\n    videoResolution: high\n", "  maxMedia: 4\n  maxMediaBytes: 1024\n", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, err := parseYAMLWorkflow(t, geminiMediaWorkflow(tc.node, tc.decisionExtra))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("parse = %v, want an error containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			node := cfg.Nodes[0]
			if node.ImageResolution != "medium" || node.VideoResolution != "high" || cfg.Decision.MaxMedia != 4 || cfg.Decision.MaxMediaBytes != 1024 {
				t.Errorf("parsed %q %q %d %d, want medium high 4 1024", node.ImageResolution, node.VideoResolution, cfg.Decision.MaxMedia, cfg.Decision.MaxMediaBytes)
			}
		})
	}
}

func TestOperatorBlockTunesTheMediaCaps(t *testing.T) {
	cfg, err := parseYAMLWorkflow(t, geminiMediaWorkflow("", "  max_media: 3\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	merged, err := MergeOperatorDecision(cfg.Decision, map[string]any{"max_media_bytes": 4096})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.MaxMedia != 3 || merged.MaxMediaBytes != 4096 || merged.APIKey != "test-key" {
		t.Errorf("merged = %+v, want the author's max_media and endpoint with the operator's max_media_bytes", merged)
	}
}

func TestDecisionNodeEscalatesWhenMediaFilesTogetherExceedTheByteCap(t *testing.T) {
	wav := mediaFixture(t, "tone.wav")
	doc := strings.Replace(geminiMediaWorkflow("    audio: [a.wav, b.wav]\n", "  max_media_bytes: "+jsonNumber(float64(2*len(wav)-1))+"\n"),
		"    default: fallback\n", escalateOnProviderError, 1)
	jev := &fakeJev{respond: geminiDepartmentAnswer("billing", 0.9)}
	store := &instrumentedStore{Service: memory.New()}
	run := newDecisionRun(t, doc, jev, true, withArtifactStore(t, store, map[string][]byte{"a.wav": wav, "b.wav": wav}), withDecisionMediaInput(t))
	logs := run.debugLogs()
	run.execute(t)

	run.assertNoProviderCall(t)
	run.assertEscalatedOn(t, decision.FailureInputTooLarge)
	logs.assertSays(t, "max_media_bytes")
	if loads := store.loads.Load(); loads != 1 {
		t.Errorf("the store read %d files, want only the first: the second is refused from its stored size", loads)
	}
}

func TestDecisionNodeSendsMediaFilesThatExactlyFillTheByteCap(t *testing.T) {
	wav := mediaFixture(t, "tone.wav")
	doc := geminiMediaWorkflow("    audio: [a.wav, b.wav]\n", "  max_media_bytes: "+jsonNumber(float64(2*len(wav)))+"\n")
	jev := &fakeJev{respond: geminiDepartmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, doc, jev, true, withArtifacts(t, map[string][]byte{"a.wav": wav, "b.wav": wav}), withDecisionMediaInput(t))
	run.execute(t)

	run.assertBranches(t, "billing", "tech", "fallback")
	if blocks := jev.geminiMediaBlocks(t); len(blocks) != 2 {
		t.Errorf("sent %d files, want both: together they equal max_media_bytes", len(blocks))
	}
}

func TestDecisionNodeEscalatesWhenAConvertedImageExceedsMaxImageBytes(t *testing.T) {
	gif := mediaFixture(t, "image.gif")
	converted, err := visionimage.ToStillPNG(gif)
	if err != nil || len(converted) <= len(gif) {
		t.Fatalf("fixture: the GIF's PNG is %d bytes (%v), want it larger than the %d-byte GIF", len(converted), err, len(gif))
	}
	doc := escalatingOpenAIImageWorkflow(`[a.gif]`, "  max_image_bytes: "+jsonNumber(float64(len(gif)))+"\n")
	jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, doc, jev, true, withArtifacts(t, map[string][]byte{"a.gif": gif}), withDecisionMediaInput(t))
	run.execute(t)

	run.assertNoProviderCall(t)
	run.assertEscalatedOn(t, decision.FailureInputTooLarge)
}

func TestDecisionNodeImagePoolAndMediaPoolAreIndependent(t *testing.T) {
	files := map[string][]byte{"a.png": pngImage(t, 4, 4), "b.png": pngImage(t, 5, 5), "call.wav": mediaFixture(t, "tone.wav")}
	cases := map[string]struct {
		node, decisionExtra string
		wantBlocks          int
	}{
		"one image and one call under max_images: 1": {"    images: [a.png]\n    audio: [call.wav]\n", "  max_images: 1\n", 2},
		"two images and one call under max_media: 1": {"    images: [a.png, b.png]\n    audio: [call.wav]\n", "  max_media: 1\n", 3},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			jev := &fakeJev{respond: geminiDepartmentAnswer("billing", 0.9)}
			run := newDecisionRun(t, geminiMediaWorkflow(tc.node, tc.decisionExtra), jev, true, withArtifacts(t, files), withDecisionMediaInput(t))
			run.execute(t)

			run.assertBranches(t, "billing", "tech", "fallback")
			if blocks := jev.geminiMediaBlocks(t); len(blocks) != tc.wantBlocks {
				t.Errorf("sent %d media blocks, want %d", len(blocks), tc.wantBlocks)
			}
		})
	}
}
