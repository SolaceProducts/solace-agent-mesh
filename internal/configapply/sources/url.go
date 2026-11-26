// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sources

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

const (
	SchemeGitHTTPS = "git+https"
	SchemeGitSSH   = "git+ssh"
	SchemeGitFile  = "git+file"
	SchemeFile     = "file"
)

type Source struct {
	Raw string

	Scheme string

	URL string

	Ref string

	Subdirectory string
}

func Parse(raw string) (Source, error) {
	original := raw
	if strings.TrimSpace(raw) == "" {
		return Source{}, errors.New("source URL is empty")
	}

	rawNoFrag, frag := splitFragment(raw)

	subdir, err := parseFragment(frag)
	if err != nil {
		return Source{}, err
	}

	if path, ok := strings.CutPrefix(rawNoFrag, "file://"); ok {
		if path == "" {
			return Source{}, fmt.Errorf("source %q: file:// requires a path", original)
		}
		return Source{
			Raw:          original,
			Scheme:       SchemeFile,
			URL:          path,
			Subdirectory: subdir,
		}, nil
	}

	scheme, ok := matchGitScheme(rawNoFrag)
	if !ok {
		return Source{}, fmt.Errorf("source %q: unsupported scheme (expected file://, git+https://, git+ssh://, or git+file://)", original)
	}

	u, err := url.Parse(rawNoFrag)
	if err != nil {
		return Source{}, fmt.Errorf("source %q: %w", original, err)
	}
	if u.Path == "" || u.Path == "/" {
		if scheme != SchemeGitFile || u.Host == "" {
			return Source{}, fmt.Errorf("source %q: missing repository path", original)
		}
	}

	ref := ""
	if i := strings.LastIndex(u.Path, "@"); i >= 0 {
		ref = u.Path[i+1:]
		u.Path = u.Path[:i]
	}
	if strings.HasPrefix(ref, "-") {
		return Source{}, fmt.Errorf("source %q: ref %q must not start with '-'", original, ref)
	}

	cloneURL := *u
	cloneURL.Scheme = strings.TrimPrefix(u.Scheme, "git+")

	return Source{
		Raw:          original,
		Scheme:       scheme,
		URL:          cloneURL.String(),
		Ref:          ref,
		Subdirectory: subdir,
	}, nil
}

func splitFragment(raw string) (string, string) {
	if before, after, ok := strings.Cut(raw, "#"); ok {
		return before, after
	}
	return raw, ""
}

func parseFragment(frag string) (string, error) {
	if frag == "" {
		return "", nil
	}
	subdir := ""
	for kv := range strings.SplitSeq(frag, "&") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return "", fmt.Errorf("malformed fragment %q (expected key=value)", frag)
		}
		if k == "subdirectory" {
			if v == "" {
				return "", fmt.Errorf("fragment %q: subdirectory= requires a value", frag)
			}
			if err := validateSubdirectory(v); err != nil {
				return "", err
			}
			subdir = v
		}
	}
	return subdir, nil
}

func validateSubdirectory(s string) error {
	if path.IsAbs(s) || strings.HasPrefix(s, "\\") || (len(s) >= 2 && s[1] == ':') {
		return fmt.Errorf("subdirectory %q must be relative", s)
	}
	cleaned := path.Clean(s)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("subdirectory %q must not escape the source root", s)
	}
	return nil
}

func matchGitScheme(raw string) (string, bool) {
	switch {
	case strings.HasPrefix(raw, "git+https://"):
		return SchemeGitHTTPS, true
	case strings.HasPrefix(raw, "git+ssh://"):
		return SchemeGitSSH, true
	case strings.HasPrefix(raw, "git+file://"):
		return SchemeGitFile, true
	}
	return "", false
}
