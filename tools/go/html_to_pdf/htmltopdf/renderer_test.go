// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package htmltopdf

import (
	"testing"

	"github.com/chromedp/cdproto/target"
)

func TestFrameOutsideGuard(t *testing.T) {
	const tab, otherTab = target.ID("tab"), target.ID("other-tab")
	refused := func(u string) bool { return u == "https://refused.example/" }
	page := &target.Info{TargetID: tab, Type: "page"}
	cases := []struct {
		name  string
		infos []*target.Info
		want  bool
	}{
		{"no frames", []*target.Info{page}, false},
		{"an error page for a refused navigation", []*target.Info{page,
			{TargetID: "f1", Type: "iframe", ParentID: tab, URL: "https://refused.example/"},
		}, false},
		{"an error page for a refused navigation written with a fragment", []*target.Info{page,
			{TargetID: "f1", Type: "iframe", ParentID: tab, URL: "https://refused.example/#section"},
		}, false},
		{"chromium's own error page", []*target.Info{page,
			{TargetID: "f1", Type: "iframe", ParentID: tab, URL: "chrome-error://chromewebdata/"},
		}, false},
		{"a frame that loaded outside the guard", []*target.Info{page,
			{TargetID: "f1", Type: "iframe", ParentID: tab, URL: "https://loaded.example/"},
		}, true},
		{"a nested frame that loaded outside the guard", []*target.Info{page,
			{TargetID: "f1", Type: "iframe", ParentID: tab, URL: "https://refused.example/"},
			{TargetID: "f2", Type: "iframe", ParentID: "f1", URL: "https://loaded.example/"},
		}, true},
		{"another tab's frame", []*target.Info{page,
			{TargetID: otherTab, Type: "page"},
			{TargetID: "f1", Type: "iframe", ParentID: otherTab, URL: "https://loaded.example/"},
		}, false},
		{"a frame whose parent chain loops", []*target.Info{page,
			{TargetID: "f1", Type: "iframe", ParentID: "f2", URL: "https://loaded.example/"},
			{TargetID: "f2", Type: "iframe", ParentID: "f1", URL: "https://loaded.example/"},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := frameOutsideGuard(tc.infos, tab, refused); got != tc.want {
				t.Errorf("frameOutsideGuard = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProxyServers(t *testing.T) {
	cases := []struct {
		name       string
		httpsProxy string
		want       string
	}{
		{"no proxy", "", blackHoleHTTPProxy},
		{"a host and port", "proxy.example:3128", blackHoleHTTPProxy + ";https=http://proxy.example:3128"},
		{"an http url", "http://proxy.example:3128", blackHoleHTTPProxy + ";https=http://proxy.example:3128"},
		{"a url with credentials", "http://user:secret@proxy.example:3128", blackHoleHTTPProxy + ";https=http://proxy.example:3128"},
		{"a socks url", "socks5://proxy.example:1080", blackHoleHTTPProxy + ";https=socks5://proxy.example:1080"},
		{"garbage", "http://[::1", blackHoleHTTPProxy},
		{"a host that smuggles a second rule", "http://proxy.example;http=127.0.0.1:1", blackHoleHTTPProxy},
		{"a host that smuggles a second proxy", "http://proxy.example,127.0.0.1:1", blackHoleHTTPProxy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HTTPS_PROXY", tc.httpsProxy)
			t.Setenv("https_proxy", "")
			if got := proxyServers(); got != tc.want {
				t.Errorf("proxyServers() = %q, want %q", got, tc.want)
			}
		})
	}
}
