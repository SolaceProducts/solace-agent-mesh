// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package smtpprovider

import (
	"errors"
	"strings"
	"testing"
)

func TestParseSMTPError_MasksRecipientKeepsDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name, reply, wantDomain, wantReason string
	}{
		{"angle-bracketed recipient", "550 5.1.1 <alice@corp.com>: Recipient address rejected: User unknown", "corp.com", "Recipient address rejected"},
		{"bare address with subdomain", "552 5.2.2 mailbox full for bob.smith+tag@sub.example.co.uk", "sub.example.co.uk", "mailbox full"},
		{"two addresses", "550 relay denied from a@x.com to b@y.com", "y.com", "relay denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSMTPError(errors.New(tc.reply)).Error()

			for _, local := range []string{"alice@", "bob.smith", "a@x.com", "b@y.com"} {
				if strings.Contains(got, local) {
					t.Errorf("recipient local part survived: %q", got)
				}
			}
			if !strings.Contains(got, tc.wantDomain) {
				t.Errorf("lost the domain, which an operator needs: %q", got)
			}
			if !strings.Contains(got, tc.wantReason) {
				t.Errorf("lost the SMTP reason text: %q", got)
			}
			if !strings.Contains(got, "***@") {
				t.Errorf("expected a masked address marker: %q", got)
			}
		})
	}
}

func TestParseSMTPError_PreservesCode(t *testing.T) {
	var se *SMTPError
	if !errors.As(parseSMTPError(errors.New("550 5.1.1 <alice@corp.com>: rejected")), &se) {
		t.Fatal("parseSMTPError no longer returns *SMTPError")
	}
	if se.Code != 550 {
		t.Errorf("code lost: got %d want 550", se.Code)
	}
}

func TestParseSMTPError_NoAddressUnchanged(t *testing.T) {
	const reply = "421 4.7.0 service temporarily unavailable"
	if got := parseSMTPError(errors.New(reply)).Error(); !strings.Contains(got, reply) {
		t.Errorf("rewrote an address-free reply: %q", got)
	}
}
