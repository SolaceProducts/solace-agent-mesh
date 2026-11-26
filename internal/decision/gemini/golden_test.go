// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gemini

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

var updateGoldens = flag.Bool("update", false, "rewrite the request goldens in testdata/")

func TestGoldenRequestsKeepTextAndImagesByteForByte(t *testing.T) {
	images := triageRequest()
	images.Media = []decision.Media{
		{Kind: decision.MediaImage, MIMEType: "image/png", Data: []byte("png-bytes")},
		{Kind: decision.MediaImage, MIMEType: "image/jpeg", Data: []byte("jpeg-bytes")},
	}
	cases := map[string]decision.Request{
		"request_text_only.json": triageRequest(),
		"request_images.json":    images,
	}
	c, err := New(Config{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	for file, req := range cases {
		t.Run(file, func(t *testing.T) {
			got, err := c.encode(req)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			path := filepath.Join("testdata", file)
			if *updateGoldens {
				if writeErr := os.WriteFile(path, got, 0o600); writeErr != nil {
					t.Fatalf("update golden: %v", writeErr)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden: %v (run with -update to create it)", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("request changed on the wire:\n got %s\nwant %s", got, want)
			}
		})
	}
}
