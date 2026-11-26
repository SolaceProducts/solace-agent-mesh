// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package imagemagicktool

import (
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestTouchesPDF(t *testing.T) {
	cases := []struct {
		name string
		p    Params
		want bool
	}{
		{"output pdf", Params{OutputFilename: "report.pdf"}, true},
		{"input pdf", Params{InputImages: []sdk.Artifact{{Filename: "doc.pdf"}}}, true},
		{"command references pdf", Params{Command: "{input[0]} scan.PDF"}, true},
		{"pdf inside quoted label", Params{OutputFilename: "out.png", Command: `-annotate +0+0 "See report.pdf for details"`}, false},
		{"images only", Params{OutputFilename: "out.png", InputImages: []sdk.Artifact{{Filename: "a.jpg"}}, Command: "-resize 50%"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := touchesPDF(c.p); got != c.want {
				t.Errorf("touchesPDF(%+v) = %v, want %v", c.p, got, c.want)
			}
		})
	}
}
