// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"regexp"
	"strings"
)

var commentRe = regexp.MustCompile(`\([^()]*\)`)

func stripComments(s string) string {
	const maxIterations = 16
	for range maxIterations {
		next := commentRe.ReplaceAllString(s, "")
		if next == s {
			return next
		}
		s = next
	}
	return s
}

var foldRe = regexp.MustCompile(`\r?\n[ \t]+`)

func ParseAuthResults(headers []string, trustedAuthservIDs []string) AuthVerdict {
	verdict := AuthVerdict{
		SPF:       AuthNone,
		DKIM:      AuthNone,
		DMARC:     AuthNone,
		RawHeader: strings.Join(headers, "\n"),
	}
	if len(headers) == 0 {
		return verdict
	}

	trusted := make(map[string]struct{}, len(trustedAuthservIDs))
	for _, id := range trustedAuthservIDs {
		trusted[strings.ToLower(strings.TrimSpace(id))] = struct{}{}
	}

	for _, h := range headers {
		unfolded := foldRe.ReplaceAllString(h, " ")
		stripped := stripComments(unfolded)

		semi := strings.Index(stripped, ";")
		if semi < 0 {
			continue
		}
		authservRaw := strings.TrimSpace(stripped[:semi])
		fields := strings.Fields(authservRaw)
		if len(fields) == 0 {
			continue
		}
		authservID := strings.ToLower(fields[0])

		if _, ok := trusted[authservID]; !ok {
			return verdict
		}

		body := stripped[semi+1:]
		dkimAcc := AuthNone
		for clause := range strings.SplitSeq(body, ";") {
			clause = strings.TrimSpace(clause)
			if clause == "" {
				continue
			}
			method, outcome, props := parseClause(clause)
			switch method {
			case "spf":
				verdict.SPF = outcome
			case "dkim":
				switch {
				case outcome == AuthPass:
					dkimAcc = AuthPass
				case dkimAcc == AuthNone:
					dkimAcc = outcome
				}
			case "dmarc":
				verdict.DMARC = outcome
				if outcome == AuthPass {
					if from, ok := props["header.from"]; ok {
						verdict.AlignedFromDomain = strings.ToLower(from)
					}
				}
			}
		}
		if dkimAcc != AuthNone {
			verdict.DKIM = dkimAcc
		}
		return verdict
	}
	return verdict
}

func parseClause(clause string) (method string, outcome AuthOutcome, props map[string]string) {
	props = make(map[string]string)
	parts := strings.Fields(clause)
	if len(parts) == 0 {
		return "", AuthNone, props
	}
	first := parts[0]
	eq := strings.Index(first, "=")
	if eq < 0 {
		return "", AuthNone, props
	}
	method = strings.ToLower(first[:eq])
	outcome = mapOutcome(first[eq+1:])
	for _, kv := range parts[1:] {
		if i := strings.Index(kv, "="); i >= 0 {
			props[strings.ToLower(kv[:i])] = strings.Trim(kv[i+1:], `";`)
		}
	}
	return
}

func mapOutcome(s string) AuthOutcome {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pass":
		return AuthPass
	case "fail":
		return AuthFail
	case "softfail":
		return AuthSoftfail
	case "neutral":
		return AuthNeutral
	case "temperror":
		return AuthTempError
	case "permerror":
		return AuthPermError
	default:
		return AuthNone
	}
}
