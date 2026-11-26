// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package logging

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var (
	urlWithQuery    = regexp.MustCompile(`(https?://[^\s"'?]+)\?[^\s"']*`)
	urlWithUserinfo = regexp.MustCompile(`(https?://)[^/\s"']+@`)
	leadingUserinfo = regexp.MustCompile(`^((?:[a-zA-Z][a-zA-Z0-9+.-]*:)?//)?[^/@\s]+@`)
)

func RedactURLQueries(s string) string {
	s = urlWithQuery.ReplaceAllString(s, "$1")
	return urlWithUserinfo.ReplaceAllString(s, "${1}xxxxx@")
}

func RedactURL(s string) string {
	if u, err := url.Parse(s); err == nil && u.Scheme != "" {
		u.RawQuery = ""
		if u.User != nil {
			u.User = url.User("xxxxx")
		}
		return u.String()
	}
	base, _, _ := strings.Cut(s, "?")
	return leadingUserinfo.ReplaceAllString(base, "${1}xxxxx@")
}

func UnwrapURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}
	return err
}
