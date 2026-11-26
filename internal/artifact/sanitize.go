// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"path/filepath"
	"strings"
	"unicode"
)

func SanitizeFilename(name string) string {
	name = filepath.Base(name)

	if name == "." || name == ".." || name == "" {
		return "_"
	}

	var b []byte
	for _, r := range name {
		if r > unicode.MaxASCII || r < 0x20 || r == 0x7F {
			b = append(b, '_')
		} else {
			b = append(b, byte(r))
		}
	}
	return string(b)
}

func BuildArtifactPrefix(loc Locator) string {
	filename, isUserScoped := ResolveFilename(loc.Filename)
	filename = SanitizeFilename(filename)
	app := SanitizeFilename(loc.AppName)
	user := SanitizeFilename(loc.UserID)
	if isUserScoped {
		return app + "/" + user + "/user/" + filename + "/"
	}
	return app + "/" + user + "/" + SanitizeFilename(loc.SessionID) + "/" + filename + "/"
}

func BuildScopePrefix(scope KeyScope) string {
	return SanitizeFilename(scope.AppName) + "/" +
		SanitizeFilename(scope.UserID) + "/" +
		SanitizeFilename(scope.SessionID)
}

func BuildUserScopePrefix(scope KeyScope) string {
	return SanitizeFilename(scope.AppName) + "/" + SanitizeFilename(scope.UserID) + "/user/"
}

func IsFilenameSafe(filename string) bool {
	trimmed := strings.TrimSpace(filename)
	if trimmed == "" {
		return false
	}
	if strings.Contains(filename, "..") {
		return false
	}
	if strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		return false
	}
	if trimmed == "." {
		return false
	}
	return true
}
