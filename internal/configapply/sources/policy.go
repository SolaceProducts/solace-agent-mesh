// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sources

import (
	"fmt"
	"regexp"
	"strings"
)

type Level int

const (
	LevelOK Level = iota
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelOK:
		return "ok"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	}
	return fmt.Sprintf("Level(%d)", int(l))
}

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

var shortSHA = regexp.MustCompile(`^[0-9a-f]{7,39}$`)

var versionTag = regexp.MustCompile(`^v?\d+(\.\d+)+([-+][0-9A-Za-z.\-]+)?$`)

func ClassifyRef(src Source, allowFloating bool) (Level, string) {
	if src.Scheme == SchemeFile {
		return LevelOK, ""
	}

	ref := strings.TrimSpace(src.Ref)
	if ref == "" || strings.EqualFold(ref, "HEAD") {
		if allowFloating {
			return LevelWarn, fmt.Sprintf("source %q has no ref (or HEAD) — non-reproducible, apply may differ across runs", src.Raw)
		}
		return LevelError, fmt.Sprintf("source %q has no ref (or HEAD) — pass --allow-floating-refs to opt in", src.Raw)
	}

	switch {
	case fullSHA.MatchString(ref):
		return LevelOK, ""
	case shortSHA.MatchString(ref):
		return LevelWarn, fmt.Sprintf("source %q uses short SHA %q — non-reproducible, apply may differ across runs", src.Raw, ref)
	case versionTag.MatchString(ref):
		return LevelOK, ""
	}

	return LevelWarn, fmt.Sprintf("source %q uses branch ref %q — non-reproducible, apply may differ across runs", src.Raw, ref)
}

func IsPinned(src Source) bool {
	if src.Scheme == SchemeFile {
		return true
	}
	ref := strings.TrimSpace(src.Ref)
	if ref == "" || strings.EqualFold(ref, "HEAD") {
		return false
	}
	if fullSHA.MatchString(ref) {
		return true
	}
	if shortSHA.MatchString(ref) {
		return false
	}
	return versionTag.MatchString(ref)
}
