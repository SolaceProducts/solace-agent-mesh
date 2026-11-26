// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httputil

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
)

type DeprecationNotices struct {
	mu       sync.Mutex
	byNotice map[string]map[string]struct{}
}

var CLINotices = &DeprecationNotices{}

func (n *DeprecationNotices) Record(resp *http.Response) {
	notice := resp.Header.Get(solaceDeprecatedHeader)
	if !printableNoticeURL(notice) || resp.Request == nil || resp.Request.URL == nil {
		return
	}
	route := resp.Request.Method + " " + resp.Request.URL.Path
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.byNotice == nil {
		n.byNotice = map[string]map[string]struct{}{}
	}
	if n.byNotice[notice] == nil {
		n.byNotice[notice] = map[string]struct{}{}
	}
	n.byNotice[notice][route] = struct{}{}
}

func printableNoticeURL(notice string) bool {
	if notice == "" {
		return false
	}
	for _, r := range notice {
		if r < ' ' || r > '~' {
			return false
		}
	}
	u, err := url.Parse(notice)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (n *DeprecationNotices) Message() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	notices := make([]string, 0, len(n.byNotice))
	for notice := range n.byNotice {
		notices = append(notices, notice)
	}
	slices.Sort(notices)
	parts := make([]string, 0, len(notices))
	for _, notice := range notices {
		routes := make([]string, 0, len(n.byNotice[notice]))
		for route := range n.byNotice[notice] {
			routes = append(routes, route)
		}
		slices.Sort(routes)
		parts = append(parts, fmt.Sprintf("%s (see %s)", strings.Join(routes, ", "), notice))
	}
	if len(parts) == 0 {
		return ""
	}
	return "this run called platform API routes that are deprecated or return deprecated fields: " + strings.Join(parts, "; ")
}

func (n *DeprecationNotices) Reset() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.byNotice = nil
}
