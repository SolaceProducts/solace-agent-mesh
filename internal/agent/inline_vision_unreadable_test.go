// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/visionimage"
)

func TestProcessFilePartsInlineVisionPassesAnUnreadableImageThrough(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.EnableInlineVision = true
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	unreadable := []byte("RIFF\x00\x00\x00\x00WEBPVP8 not a frame")
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "scan.webp", unreadable, "image/webp")

	msg := a2a.Message{
		Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/scan.webp"}}},
	}
	_, imgs, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "describe this", -1)
	if len(imgs) != 1 {
		t.Fatalf("got %d image blocks, want the unreadable image inlined unchanged", len(imgs))
	}
	if want := visionimage.DataURL("image/webp", unreadable); imgs[0].ImageURL != want {
		t.Errorf("image_url = %.60q, want the original bytes as a WebP data URL", imgs[0].ImageURL)
	}
}
