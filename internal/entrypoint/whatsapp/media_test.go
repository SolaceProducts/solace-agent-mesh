// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestVideoFilename(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		media *mediaObject
		want  string
	}{
		{nil, "video.mp4"},
		{&mediaObject{MimeType: "video/mp4"}, "video.mp4"},
		{&mediaObject{MimeType: "video/3gpp"}, "video.3gpp"},
		{&mediaObject{MimeType: "video/x-evil; boundary=../../etc"}, "video.mp4"},
	} {
		if got := videoFilename(tt.media); got != tt.want {
			t.Errorf("videoFilename(%+v) = %q, want %q", tt.media, got, tt.want)
		}
	}
}

func TestInboundVideo_RoutedAsMedia(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")

	msg := &inboundMessage{From: "15551230001", ID: "wamid.VID1", Type: "video", Video: &mediaObject{ID: "media-1", MimeType: "video/mp4"}}
	if got := a.deliver(&webhookChangeValue{}, msg); got != settleNack {
		t.Fatalf("a failed media fetch settled %v, want settleNack", got)
	}

	var notices []string
	for _, raw := range mock.rawSends() {
		var sent outboundTextMessage
		if json.Unmarshal(raw, &sent) == nil && sent.Text.Body != "" {
			notices = append(notices, sent.Text.Body)
		}
	}
	if len(notices) != 0 {
		t.Fatalf("a retryable media failure must stay silent, got notices = %v", notices)
	}
}

func TestInboundMedia_CaptionlessGetsPlaceholderText(t *testing.T) {
	t.Parallel()
	a, _, brk := mediaAdapter(t,
		`{"url":"${SRV}/dl/MEDIA1","mime_type":"video/mp4","file_size":12,"id":"MEDIA1"}`,
		"/dl/MEDIA1", []byte("MP4-BYTES"))
	q, err := brk.CreateQueue("wa-noc", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "Builder")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	msg := &inboundMessage{From: "15551230000", ID: "wamid.noc", Type: "video",
		Video: &mediaObject{ID: "MEDIA1", MimeType: "video/mp4"}}
	a.deliver(&webhookChangeValue{Contacts: []webhookContact{{WaID: "15551230000"}}, Messages: []inboundMessage{*msg}}, msg)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	bm, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("no broker publish: %v", err)
	}
	var req a2a.Request
	if err := json.Unmarshal(bm.Payload, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	params, _ := req.Params.(map[string]any)
	msgData, _ := params["message"].(map[string]any)
	parts, _ := msgData["parts"].([]any)

	var text string
	var hasFile bool
	for _, p := range parts {
		pm, _ := p.(map[string]any)
		switch pm["kind"] {
		case "text":
			text, _ = pm["text"].(string)
		case "file":
			hasFile = true
		}
	}
	if strings.TrimSpace(text) == "" {
		t.Error("caption-less media must carry a non-empty placeholder text part (empty turn otherwise)")
	}
	if !strings.Contains(text, "video") {
		t.Errorf("placeholder = %q, want it to name the media kind", text)
	}
	if !hasFile {
		t.Error("the media file part must still be forwarded")
	}
}

func TestOutboundMedia_RejectsStickerOnlyAndOpusMIME(t *testing.T) {
	t.Parallel()
	m := newOutboundMock(t)
	a := replyRig(t, m)

	if deliverMediaBytes(t, a, "", "15551230001", []byte("x"), "image/webp", "sticker.webp") {
		t.Error("image/webp is sticker-only; image sends must be rejected")
	}
	uploads, msgs := m.snapshot()
	if uploads != 0 {
		t.Error("a sticker-only type must never reach the upload endpoint")
	}
	if len(msgs) != 1 {
		t.Errorf("sends = %d, want 1 (the user notice, no doomed upload)", len(msgs))
	}
}

func TestResolveOutboundMIME_CoercesOpusToOgg(t *testing.T) {
	t.Parallel()
	if got := resolveOutboundMIME("audio/opus", "voice.opus"); got != "audio/ogg" {
		t.Errorf("audio/opus = %q, want audio/ogg", got)
	}
	if !metaAcceptedMediaMIME[resolveOutboundMIME("audio/opus", "voice.opus")] {
		t.Error("coerced opus must land on an accepted type")
	}
}

func TestClient_SendMedia_AudioAndVideoWireShapes(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	c := NewClient(&WhatsAppConfig{
		AccessToken: "tok", PhoneNumberID: "555", GraphAPIVersion: "v26.0",
		GraphAPIBaseURL: mock.server.URL, DisableRateLimiting: true,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := c.SendMedia(t.Context(), "15551230001", "med-1", "video", "clip.mp4"); err != nil {
		t.Fatalf("SendMedia video: %v", err)
	}
	if _, err := c.SendMedia(t.Context(), "15551230001", "med-2", "audio", ""); err != nil {
		t.Fatalf("SendMedia audio: %v", err)
	}

	raws := mock.rawSends()
	var vid, aud outboundMediaMessage
	_ = json.Unmarshal(raws[0], &vid)
	_ = json.Unmarshal(raws[1], &aud)
	if vid.Type != "video" || vid.Video == nil || vid.Video.ID != "med-1" || vid.Image != nil {
		t.Errorf("video wire = %+v, want type=video with video.id", vid)
	}
	if aud.Type != "audio" || aud.Audio == nil || aud.Audio.ID != "med-2" {
		t.Errorf("audio wire = %+v, want type=audio with audio.id", aud)
	}
}

func TestDisableFileUploads_DeclinesBeforeAnyGraphCall(t *testing.T) {
	t.Parallel()
	a, mock, brk := mediaAdapter(t,
		`{"url":"${SRV}/dl/MEDIA1","mime_type":"image/png","file_size":12,"id":"MEDIA1"}`,
		"/dl/MEDIA1", []byte("BYTES"))
	a.cfg.DisableFileUploads = true

	agentQ, err := brk.CreateQueue("wa-uploads-off", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(agentQ, a2a.AgentRequestTopic("test-ns", "Builder")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	for _, msg := range []*inboundMessage{
		{From: "15551230001", ID: "wamid.i", Type: "image", Image: &mediaObject{ID: "MEDIA1", MimeType: "image/png", Caption: "what is this?"}},
		{From: "15551230001", ID: "wamid.d", Type: "document", Document: &mediaObject{ID: "MEDIA1", MimeType: "application/pdf", Filename: "a.pdf"}},
		{From: "15551230001", ID: "wamid.v", Type: "video", Video: &mediaObject{ID: "MEDIA1", MimeType: "video/mp4"}},
		{From: "15551230001", ID: "wamid.a", Type: "audio", Audio: &mediaObject{ID: "MEDIA1", MimeType: "audio/ogg"}},
	} {
		if got := a.processInboundMessage(testEnvelope(a, &webhookChangeValue{}, msg), testSessionEpoch, true); got != outcomeDeclined {
			t.Errorf("%s outcome = %v, want declined", msg.Type, got)
		}
	}

	if got := mock.mediaRequests(); got != 0 {
		t.Errorf("media metadata/download requests = %d, want 0 — the bytes must never be fetched", got)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if bm, recvErr := brk.Receive(ctx, agentQ); recvErr == nil && bm != nil {
		t.Error("a task was submitted for a declined upload — the caption must be declined with the file, not forwarded on its own")
	}

	notices := replyBodies(t, a, mock)
	if len(notices) != 1 {
		t.Fatalf("notices = %d (%v), want exactly one per window", len(notices), notices)
	}
	if notices[0] != msgUploadsDisabled {
		t.Errorf("notice = %q, want the uploads-disabled decline", notices[0])
	}
}

func TestDisableFileUploads_DefaultLeavesUploadsOn(t *testing.T) {
	t.Parallel()
	a, mock, _ := mediaAdapter(t,
		`{"url":"${SRV}/dl/MEDIA1","mime_type":"image/png","file_size":12,"id":"MEDIA1"}`,
		"/dl/MEDIA1", []byte("BYTES"))

	msg := &inboundMessage{From: "15551230001", ID: "wamid.i", Type: "image",
		Image: &mediaObject{ID: "MEDIA1", MimeType: "image/png", Caption: "what is this?"}}
	if got := a.processInboundMessage(testEnvelope(a, &webhookChangeValue{}, msg), testSessionEpoch, true); got != outcomeSubmitted {
		t.Fatalf("outcome = %v, want submitted with uploads enabled by default", got)
	}
	if got := mock.mediaRequests(); got == 0 {
		t.Error("no media request was made; the control is not exercising the fetch path")
	}
}
