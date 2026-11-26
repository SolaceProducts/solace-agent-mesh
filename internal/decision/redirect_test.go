// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package decision

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
)

func redirectRequests(t *testing.T, from, to string, hops int) (*http.Request, []*http.Request) {
	t.Helper()
	first, err := url.Parse(from)
	if err != nil {
		t.Fatal(err)
	}
	next, err := url.Parse(to)
	if err != nil {
		t.Fatal(err)
	}
	via := make([]*http.Request, hops)
	for i := range via {
		via[i] = &http.Request{URL: first}
	}
	return &http.Request{URL: next}, via
}

func TestKeyHeaderSafeKeepsTheKeyOnItsOrigin(t *testing.T) {
	check := keyHeaderSafe(&http.Client{}, "x-goog-api-key").CheckRedirect
	cases := map[string]struct {
		to      string
		refused bool
	}{
		"same origin, another path": {to: "https://api.example.com/moved", refused: false},
		"https to http":             {to: "http://api.example.com/v1beta/decisions", refused: true},
		"another host":              {to: "https://other.example.com/v1beta/decisions", refused: true},
		"another port":              {to: "https://api.example.com:8443/v1beta/decisions", refused: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req, via := redirectRequests(t, "https://api.example.com/v1beta/decisions", tc.to, 1)
			err := check(req, via)
			if refused := errors.Is(err, ErrCrossOriginRedirect); refused != tc.refused {
				t.Errorf("err = %v, want refused = %v", err, tc.refused)
			}
		})
	}
}

func TestKeyHeaderSafeStopsAfterTheDefaultRedirectLimit(t *testing.T) {
	check := keyHeaderSafe(&http.Client{}, "x-goog-api-key").CheckRedirect
	req, via := redirectRequests(t, "https://api.example.com/a", "https://api.example.com/b", maxRedirects)
	if err := check(req, via); err == nil {
		t.Errorf("redirect %d was followed, want the default limit of %d", maxRedirects+1, maxRedirects)
	}
}

func TestKeyHeaderSafeKeepsTheSharedClientsPolicy(t *testing.T) {
	sharedRefusal := errors.New("shared policy")
	shared := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return sharedRefusal }}
	req, via := redirectRequests(t, "https://api.example.com/a", "https://api.example.com/b", 1)
	if err := keyHeaderSafe(shared, "x-goog-api-key").CheckRedirect(req, via); !errors.Is(err, sharedRefusal) {
		t.Errorf("err = %v, want the shared client's own policy to still apply", err)
	}
}

func TestKeyHeaderSafeLeavesABearerKeyClientAlone(t *testing.T) {
	if keyHeaderSafe(&http.Client{}, "").CheckRedirect != nil {
		t.Error("a bearer-key client gained a redirect policy")
	}
}
