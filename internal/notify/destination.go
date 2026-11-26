// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

func destinationKey(ch Channel) string {
	switch ch.Type {
	case "connector":
		if t, _ := ch.Config["tool_name"].(string); t != "" {
			return t
		}
	case "webhook":
		if u, _ := ch.Config["url"].(string); u != "" {
			return "wh_" + shortHash(u)
		}
	case "email":
		if a, _ := ch.Config["address"].(string); a != "" {
			return "em_" + shortHash(a)
		}
	}
	return ""
}

func destinationLabel(ch Channel) string {
	if lbl, _ := ch.Config["label"].(string); lbl != "" {
		return lbl
	}
	switch ch.Type {
	case "webhook":
		if raw, _ := ch.Config["url"].(string); raw != "" {
			if u, err := url.Parse(raw); err == nil && u.Host != "" {
				return u.Host
			}
			return "configured endpoint"
		}
	case "connector":
		if p := connectorPlatform(ch); p != "" {
			return titleFirst(p)
		}
	case "email":
		if a, _ := ch.Config["address"].(string); a != "" {
			return a
		}
	}
	return ""
}

func connectorPlatform(ch Channel) string {
	if t, _ := ch.Config["str_binary"].(string); t != "" {
		return t
	}
	tn, _ := ch.Config["tool_name"].(string)
	if i := strings.IndexByte(tn, '_'); i > 0 {
		return tn[:i]
	}
	return tn
}

func titleFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}
