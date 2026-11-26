// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"slices"
	"strings"
	"testing"
)

func TestTypeSafeDecisionStateImages(t *testing.T) {
	const png = "data:image/png;base64,iVBORw0KGgo="
	cases := map[string]struct {
		body    string
		want    []string
		wantErr string
	}{
		"object state":       {body: `{"state":{"ticket":"t"}}`},
		"string state":       {body: `{"state":"t"}`},
		"text-only list":     {body: `{"state":["t",{"a":1}]}`},
		"text then an image": {body: `{"state":["t",{"type":"image_url","image_url":{"url":"` + png + `"}}]}`, want: []string{"image/png"}},
		"two images in order": {
			body: `{"state":["t",{"type":"image_url","image_url":{"url":"data:image/webp;base64,UklGRg=="}},{"type":"image_url","image_url":{"url":"` + png + `"}}]}`,
			want: []string{"image/webp", "image/png"},
		},
		"image_url as a string": {body: `{"state":[{"type":"image_url","image_url":"` + png + `"}]}`, wantErr: "state[0]"},
		"remote url":            {body: `{"state":[{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]}`, wantErr: "not a base64 data URL"},
		"gif":                   {body: `{"state":[{"type":"image_url","image_url":{"url":"data:image/gif;base64,R0lG"}}]}`, wantErr: "not accepted"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := TypeSafeDecisionStateImages([]byte(c.body))
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("images = %v, want %v", got, c.want)
			}
		})
	}
}
