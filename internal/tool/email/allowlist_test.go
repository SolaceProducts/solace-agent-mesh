// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllowlistValidator_ValidateRecipients(t *testing.T) {
	v := NewAllowlistValidator([]string{"example.com", "partner.example.com"})

	tests := []struct {
		name       string
		recipients []string
		wantErr    bool
		errDomain  string
	}{
		{
			name:       "single valid recipient",
			recipients: []string{"user@example.com"},
			wantErr:    false,
		},
		{
			name:       "multiple valid recipients",
			recipients: []string{"user@example.com", "other@partner.example.com"},
			wantErr:    false,
		},
		{
			name:       "case insensitive domain",
			recipients: []string{"user@EXAMPLE.COM"},
			wantErr:    false,
		},
		{
			name:       "single invalid recipient",
			recipients: []string{"user@other.com"},
			wantErr:    true,
			errDomain:  "other.com",
		},
		{
			name:       "mixed valid and invalid",
			recipients: []string{"user@example.com", "user@other.com"},
			wantErr:    true,
			errDomain:  "other.com",
		},
		{
			name:       "empty recipients",
			recipients: []string{},
			wantErr:    false,
		},
		{
			name:       "invalid email format",
			recipients: []string{"invalid"},
			wantErr:    true,
			errDomain:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateRecipients(tt.recipients)
			if tt.wantErr {
				require.Error(t, err)
				var allowErr *AllowlistError
				require.ErrorAs(t, err, &allowErr)
				if tt.errDomain != "" {
					assert.Equal(t, tt.errDomain, allowErr.Domain)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAllowlistValidator_AllowedDomains(t *testing.T) {
	v := NewAllowlistValidator([]string{"example.com", "partner.example.com"})
	domains := v.AllowedDomains()
	assert.Len(t, domains, 2)
	assert.Contains(t, domains, "example.com")
	assert.Contains(t, domains, "partner.example.com")
}

func TestExtractDomainFromEmail(t *testing.T) {
	tests := []struct {
		email  string
		domain string
	}{
		{"user@example.com", "example.com"},
		{"user@sub.example.com", "sub.example.com"},
		{"@example.com", "example.com"},
		{"user@", ""},
		{"user", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			assert.Equal(t, tt.domain, extractDomainFromEmail(tt.email))
		})
	}
}
