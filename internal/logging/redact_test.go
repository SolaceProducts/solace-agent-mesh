// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package logging

import "testing"

func TestRedactURLQueries(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			"http client error with signed url",
			`Get "https://blob.example.com/spec.yaml?sv=2022&sig=abc123": dial tcp: timeout`,
			`Get "https://blob.example.com/spec.yaml": dial tcp: timeout`,
		},
		{
			"multiple urls",
			"fetch http://a.example/x?k=1 then https://b.example/y?t=2 failed",
			"fetch http://a.example/x then https://b.example/y failed",
		},
		{
			"url without query untouched",
			`load spec from https://api.example.com/openapi.json: HTTP 500`,
			`load spec from https://api.example.com/openapi.json: HTTP 500`,
		},
		{
			"no url untouched",
			"connection refused",
			"connection refused",
		},
		{
			"userinfo credentials masked",
			`load spec from https://alice:s3cret@api.example.com/openapi.json: HTTP 401`,
			`load spec from https://xxxxx@api.example.com/openapi.json: HTTP 401`,
		},
		{
			"userinfo and query both redacted",
			"fetch https://bob:hunter2@blob.example.com/spec.yaml?sig=abc failed",
			"fetch https://xxxxx@blob.example.com/spec.yaml failed",
		},
		{
			"password-only userinfo masked",
			`load spec from https://:s3cret@host/spec.json: HTTP 401`,
			`load spec from https://xxxxx@host/spec.json: HTTP 401`,
		},
		{
			"token-as-username masked",
			`load spec from https://GHPTOKEN@host/spec.json: HTTP 401`,
			`load spec from https://xxxxx@host/spec.json: HTTP 401`,
		},
		{
			"unencoded @ in password masked through the last @",
			`fetch "https://user:p@ssword@host/spec.json" failed`,
			`fetch "https://xxxxx@host/spec.json" failed`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RedactURLQueries(c.in); got != c.want {
				t.Errorf("RedactURLQueries(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestRedactURL(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			"credentials and query redacted",
			"https://alice:s3cret@api.example.com/spec.json?sig=abc",
			"https://xxxxx@api.example.com/spec.json",
		},
		{
			"bare token username masked",
			"https://GHPTOKEN@api.example.com/spec.json",
			"https://xxxxx@api.example.com/spec.json",
		},
		{
			"unparseable url masks whole userinfo",
			"http://user:secret@host/%zz?token=abc",
			"http://xxxxx@host/%zz",
		},
		{
			"scheme-relative url masks whole userinfo",
			"//user:secret@host/path?token=abc",
			"//xxxxx@host/path",
		},
		{
			"plain path untouched",
			"specs/petstore.yaml",
			"specs/petstore.yaml",
		},
		{
			"unencoded @ in password masked through the last @",
			"https://user:p@ssword@host/spec.json?sig=x",
			"https://xxxxx@host/spec.json",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RedactURL(c.in); got != c.want {
				t.Errorf("RedactURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
