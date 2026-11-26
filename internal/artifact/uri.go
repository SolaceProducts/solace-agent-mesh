// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

var ErrUnroutableAppName = errors.New("app name cannot be encoded into an artifact URI host")

func FormatURI(loc Locator, version int) (string, error) {
	uri := fmt.Sprintf("artifact://%s/%s/%s/%s?version=%d",
		url.PathEscape(loc.AppName),
		url.PathEscape(loc.UserID),
		url.PathEscape(loc.SessionID),
		url.PathEscape(loc.Filename),
		version,
	)
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("%w: %q: %w", ErrUnroutableAppName, loc.AppName, err)
	}
	if host, unescapeErr := url.QueryUnescape(parsed.Host); unescapeErr != nil || host != loc.AppName {
		return "", fmt.Errorf("%w: %q reads back as %q", ErrUnroutableAppName, loc.AppName, parsed.Host)
	}
	return uri, nil
}

func ParseURI(uri string) (loc Locator, version *int, ok bool) {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "artifact" {
		return Locator{}, nil, false
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	n := len(segments)
	if n < 3 || segments[n-1] == "" {
		return Locator{}, nil, false
	}
	appName, err := url.QueryUnescape(parsed.Host)
	if err != nil {
		appName = parsed.Host
	}
	userID, userErr := url.PathUnescape(segments[n-3])
	sessionID, sessionErr := url.PathUnescape(segments[n-2])
	filename, filenameErr := url.PathUnescape(segments[n-1])
	if errors.Join(userErr, sessionErr, filenameErr) != nil {
		return Locator{}, nil, false
	}
	loc = Locator{AppName: appName, UserID: userID, SessionID: sessionID, Filename: filename}
	if raw := parsed.Query().Get("version"); raw != "" {
		v, convErr := strconv.Atoi(raw)
		if convErr != nil || v < 0 {
			return Locator{}, nil, false
		}
		version = &v
	}
	return loc, version, true
}
