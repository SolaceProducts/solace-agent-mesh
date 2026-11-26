// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"testing"
)

func TestParseAuthResults_TypicalGmail(t *testing.T) {
	t.Parallel()
	headers := []string{
		`mx.google.com; spf=pass smtp.mailfrom=alice@vendor.com; dkim=pass header.d=vendor.com; dmarc=pass header.from=vendor.com`,
	}
	got := ParseAuthResults(headers, []string{"mx.google.com"})
	if got.SPF != AuthPass || got.DKIM != AuthPass || got.DMARC != AuthPass {
		t.Errorf("got %+v, want all pass", got)
	}
	if got.AlignedFromDomain != "vendor.com" {
		t.Errorf("AlignedFromDomain = %q, want vendor.com", got.AlignedFromDomain)
	}
}

func TestParseAuthResults_TypicalOffice365(t *testing.T) {
	t.Parallel()
	headers := []string{
		`protection.outlook.com; spf=pass (sender IP is 1.2.3.4) smtp.mailfrom=vendor.com; dkim=pass (signature was verified) header.d=vendor.com; dmarc=pass action=none header.from=vendor.com;`,
	}
	got := ParseAuthResults(headers, []string{"protection.outlook.com"})
	if got.DMARC != AuthPass {
		t.Errorf("DMARC = %q, want pass", got.DMARC)
	}
}

func TestParseAuthResults_HeaderMissing(t *testing.T) {
	t.Parallel()
	got := ParseAuthResults(nil, []string{"protection.outlook.com"})
	if got.SPF != AuthNone || got.DKIM != AuthNone || got.DMARC != AuthNone {
		t.Errorf("missing header should be all-AuthNone, got %+v", got)
	}
}

func TestParseAuthResults_ForgedAuthservIgnored(t *testing.T) {
	t.Parallel()
	headers := []string{
		`mx.attacker.example; spf=pass; dkim=pass; dmarc=pass`,
	}
	got := ParseAuthResults(headers, []string{"protection.outlook.com"})
	if got.DMARC != AuthNone {
		t.Errorf("forged authserv-id should be ignored; DMARC = %q, want none", got.DMARC)
	}
}

func TestParseAuthResults_NonTrustedTopHopRejectsAll(t *testing.T) {
	t.Parallel()
	headers := []string{
		`mx.attacker.example; spf=pass; dkim=pass; dmarc=pass`,
		`protection.outlook.com; spf=fail; dkim=fail; dmarc=fail`,
	}
	got := ParseAuthResults(headers, []string{"protection.outlook.com"})
	if got.SPF != AuthNone || got.DKIM != AuthNone || got.DMARC != AuthNone {
		t.Errorf("non-trusted top-hop should produce all-AuthNone, got %+v", got)
	}
}

func TestParseAuthResults_TrustedTopHopWinsBelowIgnored(t *testing.T) {
	t.Parallel()
	headers := []string{
		`protection.outlook.com; spf=fail; dkim=fail; dmarc=fail`,
		`protection.outlook.com; spf=pass; dkim=pass; dmarc=pass header.from=vendor.com`,
	}
	got := ParseAuthResults(headers, []string{"protection.outlook.com"})
	if got.DMARC != AuthFail {
		t.Errorf("topmost trusted hop should win; DMARC = %q, want fail", got.DMARC)
	}
}

func TestParseAuthResults_MultipleDKIMSignaturesAtLeastOnePass(t *testing.T) {
	t.Parallel()
	headers := []string{
		`protection.outlook.com; dkim=fail header.d=relay.example; dkim=pass header.d=vendor.com; dmarc=pass header.from=vendor.com`,
	}
	got := ParseAuthResults(headers, []string{"protection.outlook.com"})
	if got.DKIM != AuthPass {
		t.Errorf("DKIM with one pass signature should = pass, got %q", got.DKIM)
	}
}

func TestParseAuthResults_DKIMFirstNonPassWins(t *testing.T) {
	t.Parallel()
	headers := []string{
		`protection.outlook.com; dkim=fail header.d=relay.example; dkim=softfail header.d=other.example; dmarc=pass header.from=vendor.com`,
	}
	got := ParseAuthResults(headers, []string{"protection.outlook.com"})
	if got.DKIM != AuthFail {
		t.Errorf("first non-pass should win; DKIM = %q, want fail", got.DKIM)
	}
}

func TestParseAuthResults_FoldedHeader(t *testing.T) {
	t.Parallel()
	folded := "protection.outlook.com;\r\n\tspf=pass smtp.mailfrom=vendor.com;\r\n\tdkim=pass header.d=vendor.com;\r\n\tdmarc=pass header.from=vendor.com"
	got := ParseAuthResults([]string{folded}, []string{"protection.outlook.com"})
	if got.DMARC != AuthPass {
		t.Errorf("folded header parsing failed; DMARC = %q", got.DMARC)
	}
}

func TestParseAuthResults_UnknownOutcomeMarkedNone(t *testing.T) {
	t.Parallel()
	headers := []string{
		`protection.outlook.com; spf=somethingweird; dmarc=pass header.from=vendor.com`,
	}
	got := ParseAuthResults(headers, []string{"protection.outlook.com"})
	if got.SPF != AuthNone {
		t.Errorf("unknown outcome should be AuthNone, got %q", got.SPF)
	}
	if got.DMARC != AuthPass {
		t.Errorf("DMARC parsing should still work; got %q", got.DMARC)
	}
}

func TestParseAuthResults_DMARCInformationalPolicyIgnored(t *testing.T) {
	t.Parallel()
	headers := []string{
		`protection.outlook.com; dmarc=pass action=quarantine header.from=vendor.com`,
	}
	got := ParseAuthResults(headers, []string{"protection.outlook.com"})
	if got.DMARC != AuthPass {
		t.Errorf("informational policy field should not affect outcome; DMARC = %q", got.DMARC)
	}
}
