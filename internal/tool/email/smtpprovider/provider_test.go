// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package smtpprovider

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProvider(t *testing.T) {
	config := Config{
		Host:     "smtp.example.com",
		Port:     587,
		TLS:      TLSModeSTARTTLS,
		AuthType: AuthTypePlain,
		Username: "user",
		Password: "pass",
	}

	provider := NewProvider(config, "sender@example.com")

	assert.NotNil(t, provider)
	assert.Equal(t, config, provider.config)
	assert.Equal(t, "sender@example.com", provider.envelopeFrom)
}

func TestProvider_Close(t *testing.T) {
	provider := NewProvider(Config{}, "sender@example.com")
	err := provider.Close()
	assert.NoError(t, err)
}

func TestMessage_allRecipients(t *testing.T) {
	tests := []struct {
		name     string
		msg      *Message
		expected []string
	}{
		{
			name: "to only",
			msg: &Message{
				To: []string{"to@example.com"},
			},
			expected: []string{"to@example.com"},
		},
		{
			name: "to and cc",
			msg: &Message{
				To: []string{"to@example.com"},
				CC: []string{"cc@example.com"},
			},
			expected: []string{"to@example.com", "cc@example.com"},
		},
		{
			name: "all recipients",
			msg: &Message{
				To:  []string{"to1@example.com", "to2@example.com"},
				CC:  []string{"cc@example.com"},
				BCC: []string{"bcc@example.com"},
			},
			expected: []string{"to1@example.com", "to2@example.com", "cc@example.com", "bcc@example.com"},
		},
		{
			name:     "empty message",
			msg:      &Message{},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.msg.allRecipients()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAuthError(t *testing.T) {
	err := &AuthError{Message: "invalid credentials"}
	assert.Equal(t, "authentication failed: invalid credentials", err.Error())
}

func TestConnectionError(t *testing.T) {
	err := &ConnectionError{
		Host:    "smtp.example.com",
		Port:    587,
		Message: "connection refused",
	}
	assert.Equal(t, "connection to smtp.example.com:587 failed: connection refused", err.Error())
}

func TestSMTPError(t *testing.T) {
	err := &SMTPError{Code: 550, Message: "User unknown"}
	assert.Equal(t, "SMTP error 550: User unknown", err.Error())
}

func TestParseSMTPError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		expectedCode int
	}{
		{
			name:         "nil error",
			err:          nil,
			expectedCode: 0,
		},
		{
			name:         "error with code",
			err:          errors.New("550 User unknown"),
			expectedCode: 550,
		},
		{
			name:         "error without code",
			err:          errors.New("connection reset"),
			expectedCode: 0,
		},
		{
			name:         "short error",
			err:          errors.New("er"),
			expectedCode: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseSMTPError(tt.err)
			if tt.err == nil {
				assert.Nil(t, result)
			} else {
				smtpErr, ok := result.(*SMTPError)
				require.True(t, ok)
				assert.Equal(t, tt.expectedCode, smtpErr.Code)
			}
		})
	}
}

func TestXOAuth2Auth_Start(t *testing.T) {
	auth := &xoauth2Auth{
		username:    "user@example.com",
		accessToken: "test-token-123",
	}

	proto, response, err := auth.Start(nil)

	assert.NoError(t, err)
	assert.Equal(t, "XOAUTH2", proto)
	expected := "user=user@example.com\x01auth=Bearer test-token-123\x01\x01"
	assert.Equal(t, expected, string(response))
}

func TestXOAuth2Auth_Next(t *testing.T) {
	auth := &xoauth2Auth{
		username:    "user@example.com",
		accessToken: "test-token-123",
	}

	response, err := auth.Next([]byte("error"), true)
	assert.NoError(t, err)
	assert.Equal(t, []byte{}, response)

	response, err = auth.Next(nil, false)
	assert.NoError(t, err)
	assert.Nil(t, response)
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		email  string
		domain string
	}{
		{"user@example.com", "example.com"},
		{"user@sub.example.com", "sub.example.com"},
		{"@example.com", "example.com"},
		{"user@", ""},
		{"user", "localhost"},
		{"", "localhost"},
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			assert.Equal(t, tt.domain, extractDomain(tt.email))
		})
	}
}

func TestEncodeSubject(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantASCII bool
	}{
		{
			name:      "ascii only",
			input:     "Hello World",
			wantASCII: true,
		},
		{
			name:      "with unicode",
			input:     "Hello 世界",
			wantASCII: false,
		},
		{
			name:      "with emoji",
			input:     "Hello 😀",
			wantASCII: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := encodeSubject(tt.input)
			if tt.wantASCII {
				assert.Equal(t, tt.input, encoded)
			} else {
				assert.True(t, strings.HasPrefix(encoded, "=?utf-8?"))
			}
		})
	}
}

func TestGenerateBoundary(t *testing.T) {
	b1 := generateBoundary()

	assert.True(t, strings.HasPrefix(b1, "----=_Part_"))

	assert.GreaterOrEqual(t, len(b1), len("----=_Part_")+10)
}

func TestBase64LineWriter(t *testing.T) {
	var buf strings.Builder
	w := &base64LineWriter{w: &buf}

	data := strings.Repeat("A", 100)
	n, err := w.Write([]byte(data))

	assert.NoError(t, err)
	assert.Equal(t, 100, n)

	output := buf.String()
	assert.Contains(t, output, "\r\n")

	lines := strings.Split(output, "\r\n")
	for _, line := range lines {
		assert.LessOrEqual(t, len(line), 76)
	}
}

func TestBase64LineWriter_MultiplWrites(t *testing.T) {
	var buf strings.Builder
	w := &base64LineWriter{w: &buf}

	for i := 0; i < 10; i++ {
		_, err := w.Write([]byte(strings.Repeat("B", 20)))
		assert.NoError(t, err)
	}

	output := buf.String()
	lines := strings.Split(output, "\r\n")
	for _, line := range lines {
		assert.LessOrEqual(t, len(line), 76)
	}
}

func TestLoginAuth(t *testing.T) {
	auth := &loginAuth{
		username: "user",
		password: "pass",
	}

	proto, initial, err := auth.Start(nil)
	assert.NoError(t, err)
	assert.Equal(t, "LOGIN", proto)
	assert.Nil(t, initial)

	resp, err := auth.Next([]byte("USERNAME:"), true)
	assert.NoError(t, err)
	assert.Equal(t, []byte("user"), resp)

	resp, err = auth.Next([]byte("PASSWORD:"), true)
	assert.NoError(t, err)
	assert.Equal(t, []byte("pass"), resp)

	resp, err = auth.Next(nil, false)
	assert.NoError(t, err)
	assert.Nil(t, resp)

	_, err = auth.Next([]byte("UNEXPECTED:"), true)
	assert.Error(t, err)
}

func TestLoginAuth_CaseInsensitive(t *testing.T) {
	auth := &loginAuth{
		username: "testuser",
		password: "testpass",
	}

	resp, err := auth.Next([]byte("username:"), true)
	assert.NoError(t, err)
	assert.Equal(t, []byte("testuser"), resp)

	resp, err = auth.Next([]byte("password:"), true)
	assert.NoError(t, err)
	assert.Equal(t, []byte("testpass"), resp)
}

func TestProvider_BuildEmail_PlainText(t *testing.T) {
	provider := NewProvider(Config{}, "sender@example.com")

	msg := &Message{
		To:        []string{"to@example.com"},
		Subject:   "Test Subject",
		BodyPlain: "Hello, World!",
	}

	data, err := provider.buildEmail(msg, "<test-id@example.com>")

	require.NoError(t, err)
	emailStr := string(data)

	assert.Contains(t, emailStr, "From: sender@example.com")
	assert.Contains(t, emailStr, "To: to@example.com")
	assert.Contains(t, emailStr, "Subject: Test Subject")
	assert.Contains(t, emailStr, "Message-Id: <test-id@example.com>")
	assert.Contains(t, emailStr, "Content-Type: text/plain; charset=utf-8")
	assert.Contains(t, emailStr, "Mime-Version: 1.0")
}

func TestProvider_BuildEmail_WithCC(t *testing.T) {
	provider := NewProvider(Config{}, "sender@example.com")

	msg := &Message{
		To:        []string{"to@example.com"},
		CC:        []string{"cc1@example.com", "cc2@example.com"},
		Subject:   "Test Subject",
		BodyPlain: "Hello!",
	}

	data, err := provider.buildEmail(msg, "<test-id@example.com>")

	require.NoError(t, err)
	emailStr := string(data)

	assert.Contains(t, emailStr, "Cc: cc1@example.com, cc2@example.com")
}

func TestProvider_BuildEmail_HTMLOnly(t *testing.T) {
	provider := NewProvider(Config{}, "sender@example.com")

	msg := &Message{
		To:        []string{"to@example.com"},
		Subject:   "Test Subject",
		BodyPlain: "Plain text",
		BodyHTML:  "<html><body>Hello!</body></html>",
	}

	data, err := provider.buildEmail(msg, "<test-id@example.com>")

	require.NoError(t, err)
	emailStr := string(data)

	assert.Contains(t, emailStr, "multipart/alternative")
	assert.Contains(t, emailStr, "text/plain")
	assert.Contains(t, emailStr, "text/html")
}

func TestProvider_BuildEmail_WithAttachments(t *testing.T) {
	provider := NewProvider(Config{}, "sender@example.com")

	msg := &Message{
		To:        []string{"to@example.com"},
		Subject:   "Test Subject",
		BodyPlain: "See attachment",
		Attachments: []Attachment{
			{
				Filename: "test.txt",
				Content:  []byte("file content"),
				MIMEType: "text/plain",
			},
		},
	}

	data, err := provider.buildEmail(msg, "<test-id@example.com>")

	require.NoError(t, err)
	emailStr := string(data)

	assert.Contains(t, emailStr, "multipart/mixed")
	assert.Contains(t, emailStr, "attachment; filename=\"test.txt\"")
	assert.Contains(t, emailStr, "Content-Transfer-Encoding: base64")
}

func TestProvider_BuildEmail_WithAttachmentsAndHTML(t *testing.T) {
	provider := NewProvider(Config{}, "sender@example.com")

	msg := &Message{
		To:        []string{"to@example.com"},
		Subject:   "Test Subject",
		BodyPlain: "Plain text",
		BodyHTML:  "<html><body>HTML</body></html>",
		Attachments: []Attachment{
			{
				Filename: "doc.pdf",
				Content:  []byte("pdf content"),
				MIMEType: "application/pdf",
			},
		},
	}

	data, err := provider.buildEmail(msg, "<test-id@example.com>")

	require.NoError(t, err)
	emailStr := string(data)

	assert.Contains(t, emailStr, "multipart/mixed")
	assert.Contains(t, emailStr, "multipart/alternative")
	assert.Contains(t, emailStr, "text/plain")
	assert.Contains(t, emailStr, "text/html")
	assert.Contains(t, emailStr, "application/pdf")
}

func TestProvider_BuildEmail_WithAttachmentNoMIMEType(t *testing.T) {
	provider := NewProvider(Config{}, "sender@example.com")

	msg := &Message{
		To:        []string{"to@example.com"},
		Subject:   "Test",
		BodyPlain: "Body",
		Attachments: []Attachment{
			{
				Filename: "unknown.bin",
				Content:  []byte("binary data"),
				MIMEType: "",
			},
		},
	}

	data, err := provider.buildEmail(msg, "<test-id@example.com>")

	require.NoError(t, err)
	emailStr := string(data)

	assert.Contains(t, emailStr, "application/octet-stream")
}

func TestProvider_BuildEmail_Threading(t *testing.T) {
	provider := NewProvider(Config{}, "sender@example.com")

	msg := &Message{
		To:         []string{"to@example.com"},
		Subject:    "Re: Original",
		BodyPlain:  "Reply text",
		InReplyTo:  "<original-id@example.com>",
		References: []string{"<ref1@example.com>", "<ref2@example.com>"},
	}

	data, err := provider.buildEmail(msg, "<reply-id@example.com>")

	require.NoError(t, err)
	emailStr := string(data)

	assert.Contains(t, emailStr, "In-Reply-To: <original-id@example.com>")
	assert.Contains(t, emailStr, "References: <ref1@example.com> <ref2@example.com> <original-id@example.com>")
}

func TestProvider_BuildAuth_Plain(t *testing.T) {
	provider := NewProvider(Config{
		Host:     "smtp.example.com",
		AuthType: AuthTypePlain,
		Username: "user",
		Password: "pass",
	}, "sender@example.com")

	auth, err := provider.buildAuth(t.Context())

	require.NoError(t, err)
	require.NotNil(t, auth)
}

func TestProvider_BuildAuth_Login(t *testing.T) {
	provider := NewProvider(Config{
		AuthType: AuthTypeLogin,
		Username: "user",
		Password: "pass",
	}, "sender@example.com")

	auth, err := provider.buildAuth(t.Context())

	require.NoError(t, err)
	require.NotNil(t, auth)

	loginAuth, ok := auth.(*loginAuth)
	require.True(t, ok)
	assert.Equal(t, "user", loginAuth.username)
	assert.Equal(t, "pass", loginAuth.password)
}

func TestProvider_BuildAuth_None(t *testing.T) {
	provider := NewProvider(Config{
		AuthType: AuthTypeNone,
	}, "sender@example.com")

	auth, err := provider.buildAuth(t.Context())

	require.NoError(t, err)
	assert.Nil(t, auth)
}

func TestProvider_BuildAuth_OAuthMicrosoft_MissingConfig(t *testing.T) {
	provider := NewProvider(Config{
		AuthType: AuthTypeOAuthMicrosoft,
		OAuth:    nil,
	}, "sender@example.com")

	_, err := provider.buildAuth(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "OAuth configuration is required")
}

func TestProvider_BuildAuth_OAuthGoogle_MissingConfig(t *testing.T) {
	provider := NewProvider(Config{
		AuthType: AuthTypeOAuthGoogle,
		OAuth:    nil,
	}, "sender@example.com")

	_, err := provider.buildAuth(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "OAuth configuration is required")
}

func TestGetMicrosoftAccessToken_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token": "test-token", "token_type": "Bearer", "expires_in": 3600}`))
	}))
	server.Close()

}

func TestGetMicrosoftAccessToken_Error(t *testing.T) {
	provider := NewProvider(Config{
		AuthType: AuthTypeOAuthMicrosoft,
		OAuth: &OAuthConfig{
			Provider:     "microsoft",
			TenantID:     "invalid-tenant",
			ClientID:     "invalid-client",
			ClientSecret: "invalid-secret",
			Email:        "user@example.com",
		},
	}, "sender@example.com")

	ctx, cancel := context.WithTimeout(t.Context(), 100)
	defer cancel()

	_, err := provider.buildAuth(ctx)

	require.Error(t, err)
}

func TestGetGoogleAccessToken_Error(t *testing.T) {
	provider := NewProvider(Config{
		AuthType: AuthTypeOAuthGoogle,
		OAuth: &OAuthConfig{
			Provider:     "google",
			ClientID:     "invalid-client",
			ClientSecret: "invalid-secret",
			RefreshToken: "invalid-token",
			Email:        "user@example.com",
		},
	}, "sender@example.com")

	ctx, cancel := context.WithTimeout(t.Context(), 100)
	defer cancel()

	_, err := provider.buildAuth(ctx)

	require.Error(t, err)
}

func TestTLSModeConstants(t *testing.T) {
	assert.Equal(t, TLSMode("none"), TLSModeNone)
	assert.Equal(t, TLSMode("starttls"), TLSModeSTARTTLS)
	assert.Equal(t, TLSMode("tls"), TLSModeTLS)
}

func TestAuthTypeConstants(t *testing.T) {
	assert.Equal(t, AuthType("none"), AuthTypeNone)
	assert.Equal(t, AuthType("plain"), AuthTypePlain)
	assert.Equal(t, AuthType("login"), AuthTypeLogin)
	assert.Equal(t, AuthType("oauth_microsoft"), AuthTypeOAuthMicrosoft)
	assert.Equal(t, AuthType("oauth_google"), AuthTypeOAuthGoogle)
}

func TestWriteHeaders(t *testing.T) {
	var buf bytes.Buffer
	headers := make(textproto.MIMEHeader)
	headers["From"] = []string{"sender@example.com"}
	headers["To"] = []string{"recipient@example.com"}

	writeHeaders(&buf, headers)

	output := buf.String()
	assert.Contains(t, output, "From: sender@example.com")
	assert.Contains(t, output, "To: recipient@example.com")
	assert.True(t, strings.HasSuffix(output, "\r\n"))
}

func TestProvider_BuildEmail_UnicodeSubject(t *testing.T) {
	provider := NewProvider(Config{}, "sender@example.com")

	msg := &Message{
		To:        []string{"to@example.com"},
		Subject:   "日本語のテスト",
		BodyPlain: "Hello",
	}

	data, err := provider.buildEmail(msg, "<test-id@example.com>")

	require.NoError(t, err)
	emailStr := string(data)

	assert.Contains(t, emailStr, "Subject: =?utf-8?")
}

func TestProvider_BuildEmail_MultipleRecipients(t *testing.T) {
	provider := NewProvider(Config{}, "sender@example.com")

	msg := &Message{
		To:        []string{"to1@example.com", "to2@example.com", "to3@example.com"},
		Subject:   "Test",
		BodyPlain: "Hello",
	}

	data, err := provider.buildEmail(msg, "<test-id@example.com>")

	require.NoError(t, err)
	emailStr := string(data)

	assert.Contains(t, emailStr, "To: to1@example.com, to2@example.com, to3@example.com")
}
