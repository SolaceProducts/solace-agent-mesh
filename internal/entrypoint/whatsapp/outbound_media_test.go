// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"bytes"
	"strings"
	"testing"
)

func TestOutboundMedia_AudioAndVideoUseNativeMessageTypes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, mime, wantType string
	}{
		{"voice.mp3", "audio/mpeg", "audio"},
		{"clip.mp4", "video/mp4", "video"},
		{"pic.png", "image/png", "image"},
		{"doc.pdf", "application/pdf", "document"},
	}
	for _, c := range cases {
		t.Run(c.wantType, func(t *testing.T) {
			m := newOutboundMock(t)
			a := replyRig(t, m)

			if !deliverMediaBytes(t, a, taskT1, "15551230000", []byte("payload"), c.mime, c.name) {
				t.Fatalf("queueing %s was refused", c.mime)
			}
			_, msgs := m.snapshot()
			if len(msgs) != 1 || msgs[0]["type"] != c.wantType {
				t.Fatalf("message type = %v, want %q", msgs[0]["type"], c.wantType)
			}
			if msgs[0][c.wantType] == nil {
				t.Fatalf("payload missing the %q body: %v", c.wantType, msgs[0])
			}
		})
	}
}

func TestOutboundMedia_RejectsStickerOnlyTypesBeforeUpload(t *testing.T) {
	t.Parallel()
	for _, mime := range []string{"image/webp"} {
		t.Run(mime, func(t *testing.T) {
			m := newOutboundMock(t)
			a := replyRig(t, m)

			if deliverMediaBytes(t, a, taskT1, "15551230000", []byte("payload"), mime, "f.bin") {
				t.Fatalf("%s must be rejected before upload", mime)
			}
			uploads, msgs := m.snapshot()
			if uploads != 0 {
				t.Fatalf("%s must not reach the upload endpoint", mime)
			}
			if len(msgs) != 1 || !strings.Contains(msgs[0]["text"].(map[string]any)["body"].(string), "doesn't accept") {
				t.Fatalf("expected a can't-send notice, got %v", msgs)
			}
		})
	}
}

func TestOutboundMedia_PerTypeSizeCaps(t *testing.T) {
	t.Parallel()
	sixMB := bytes.Repeat([]byte("x"), 6*1024*1024)

	m := newOutboundMock(t)
	a := replyRig(t, m)
	if deliverMediaBytes(t, a, taskT1, "15551230000", sixMB, "image/png", "big.png") {
		t.Fatal("a 6 MB image exceeds Meta's 5 MB image cap and must be declined")
	}
	uploads, msgs := m.snapshot()
	if uploads != 0 {
		t.Fatal("over-cap image must not be uploaded")
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0]["text"].(map[string]any)["body"].(string), "too large") {
		t.Fatalf("expected a too-large notice, got %v", msgs)
	}

	m2 := newOutboundMock(t)
	a2 := replyRig(t, m2)
	if !deliverMediaBytes(t, a2, taskT1, "15551230000", sixMB, "application/pdf", "big.pdf") {
		t.Fatal("a 6 MB document is within both caps and must send")
	}
}

func TestOutboundMediaCapBytes_MinOfConfigAndType(t *testing.T) {
	t.Parallel()
	m := newOutboundMock(t)
	a := replyRig(t, m)

	a.cfg.MaxMediaSizeMB = 100
	if got := a.outboundMediaCapBytes("image"); got != 5*1024*1024 {
		t.Fatalf("image cap = %d, want Meta's 5 MB", got)
	}
	if got := a.outboundMediaCapBytes("document"); got != 100*1024*1024 {
		t.Fatalf("document cap = %d, want config 100 MB", got)
	}
	a.cfg.MaxMediaSizeMB = 2
	if got := a.outboundMediaCapBytes("image"); got != 2*1024*1024 {
		t.Fatalf("image cap = %d, want tighter config 2 MB", got)
	}
}

func TestSendInteractiveList_SectionCarriesTitle(t *testing.T) {
	t.Parallel()
	mock := newRawMock(t)
	cfg := &WhatsAppConfig{
		AccessToken: "tok", PhoneNumberID: "555111", GraphAPIVersion: "v26.0",
		GraphAPIBaseURL: mock.server.URL, DisableRateLimiting: true,
	}
	c := NewClient(cfg, discardLogger())

	if _, err := c.SendInteractiveList(t.Context(), "155", "Pick one", "Choose", []listRow{{ID: "opt:0", Title: "A"}}); err != nil {
		t.Fatalf("SendInteractiveList: %v", err)
	}
	bodies := mock.all()
	if len(bodies) != 1 || !bytes.Contains(bodies[0], []byte(`"title":"Options"`)) {
		t.Fatalf("list section must carry a title, got %s", bodies[0])
	}
}
