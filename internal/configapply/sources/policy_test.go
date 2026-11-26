// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sources

import (
	"strings"
	"testing"
)

func TestRefPolicyTagOK(t *testing.T) {
	cases := []string{"v1.4.2", "1.0.0", "v2.0.0-rc1", "1.4"}
	for _, ref := range cases {
		level, _ := ClassifyRef(Source{Scheme: SchemeGitHTTPS, Ref: ref}, false)
		if level != LevelOK {
			t.Errorf("ClassifyRef(%q) = %v; want LevelOK", ref, level)
		}
	}
}

func TestRefPolicySingleComponentVersionIsBranch(t *testing.T) {
	for _, ref := range []string{"v1", "v2", "1", "2"} {
		level, _ := ClassifyRef(Source{Scheme: SchemeGitHTTPS, Ref: ref, Raw: "git+https://x/y@" + ref}, false)
		if level != LevelWarn {
			t.Errorf("ClassifyRef(%q) = %v; want LevelWarn (single-component versions are branches)", ref, level)
		}
		if IsPinned(Source{Scheme: SchemeGitHTTPS, Ref: ref}) {
			t.Errorf("IsPinned(%q) = true; want false", ref)
		}
	}
}

func TestRefPolicyFullSHAOK(t *testing.T) {
	level, _ := ClassifyRef(Source{
		Scheme: SchemeGitHTTPS,
		Ref:    "0123456789abcdef0123456789abcdef01234567",
	}, false)
	if level != LevelOK {
		t.Errorf("full SHA = %v; want LevelOK", level)
	}
}

func TestRefPolicyBranchWarn(t *testing.T) {
	cases := []string{"main", "develop", "feature/foo"}
	for _, ref := range cases {
		level, msg := ClassifyRef(Source{Scheme: SchemeGitHTTPS, Ref: ref, Raw: "git+https://x/y@" + ref}, false)
		if level != LevelWarn {
			t.Errorf("branch %q level = %v; want LevelWarn", ref, level)
		}
		if !strings.Contains(msg, "non-reproducible") {
			t.Errorf("branch %q msg = %q; want 'non-reproducible'", ref, msg)
		}
	}
}

func TestRefPolicyShortSHAWarn(t *testing.T) {
	level, msg := ClassifyRef(Source{
		Scheme: SchemeGitHTTPS,
		Ref:    "9c4e8a1",
		Raw:    "git+https://x/y@9c4e8a1",
	}, false)
	if level != LevelWarn {
		t.Errorf("short SHA level = %v; want LevelWarn", level)
	}
	if !strings.Contains(msg, "short SHA") {
		t.Errorf("short SHA msg = %q; want it to mention 'short SHA'", msg)
	}
}

func TestRefPolicyMissingRefError(t *testing.T) {
	for _, ref := range []string{"", "HEAD", "head"} {
		level, msg := ClassifyRef(Source{Scheme: SchemeGitHTTPS, Ref: ref, Raw: "git+https://x/y"}, false)
		if level != LevelError {
			t.Errorf("ref=%q level = %v; want LevelError", ref, level)
		}
		if !strings.Contains(msg, "allow-floating-refs") {
			t.Errorf("ref=%q msg = %q; want it to mention --allow-floating-refs", ref, msg)
		}
	}
}

func TestRefPolicyAllowFloating(t *testing.T) {
	for _, ref := range []string{"", "HEAD"} {
		level, msg := ClassifyRef(Source{Scheme: SchemeGitHTTPS, Ref: ref, Raw: "git+https://x/y"}, true)
		if level != LevelWarn {
			t.Errorf("ref=%q with allowFloating level = %v; want LevelWarn", ref, level)
		}
		if !strings.Contains(msg, "non-reproducible") {
			t.Errorf("ref=%q msg = %q", ref, msg)
		}
	}
}

func TestRefPolicyFileSchemeOK(t *testing.T) {
	level, msg := ClassifyRef(Source{Scheme: SchemeFile, Raw: "file://../local"}, false)
	if level != LevelOK {
		t.Errorf("file scheme level = %v; want LevelOK", level)
	}
	if msg != "" {
		t.Errorf("file scheme msg = %q; want empty", msg)
	}
}

func TestIsPinned(t *testing.T) {
	cases := []struct {
		src  Source
		want bool
	}{
		{Source{Scheme: SchemeFile}, true},
		{Source{Scheme: SchemeGitHTTPS, Ref: "v1.0.0"}, true},
		{Source{Scheme: SchemeGitHTTPS, Ref: "0123456789abcdef0123456789abcdef01234567"}, true},
		{Source{Scheme: SchemeGitHTTPS, Ref: "main"}, false},
		{Source{Scheme: SchemeGitHTTPS, Ref: "9c4e8a1"}, false},
		{Source{Scheme: SchemeGitHTTPS, Ref: ""}, false},
		{Source{Scheme: SchemeGitHTTPS, Ref: "HEAD"}, false},
	}
	for _, c := range cases {
		if got := IsPinned(c.src); got != c.want {
			t.Errorf("IsPinned(%+v) = %v; want %v", c.src, got, c.want)
		}
	}
}
