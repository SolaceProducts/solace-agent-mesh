// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/emersion/go-sasl"
	"golang.org/x/oauth2"
)

type fakeTokenSource struct {
	mu    sync.Mutex
	token string
	err   error
	calls int
}

func (f *fakeTokenSource) Token() (*oauth2.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &oauth2.Token{
		AccessToken: f.token,
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(1 * time.Hour),
	}, nil
}

func (f *fakeTokenSource) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type xoauth2MemSession struct {
	imapserver.Session

	expectedUser    string
	verifiedToken   *string
	postAuthLoginPw string
	mu              sync.Mutex
}

func (s *xoauth2MemSession) recordToken(tok string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verifiedToken = &tok
}

func (s *xoauth2MemSession) AuthenticateMechanisms() []string {
	return []string{xoauth2Mech}
}

func (s *xoauth2MemSession) Authenticate(mech string) (sasl.Server, error) {
	if mech != xoauth2Mech {
		return nil, fmt.Errorf("test: unsupported SASL mechanism %q", mech)
	}
	return newXoauth2Server(func(user, token string) error {
		if user != s.expectedUser {
			return errors.New("xoauth2: user mismatch")
		}
		if token == "" {
			return errors.New("xoauth2: empty token")
		}
		s.recordToken(token)
		return s.Login(user, s.postAuthLoginPw)
	}), nil
}

func startXOAUTH2MemIMAPServer(t *testing.T) (string, int, *[]*xoauth2MemSession) {
	t.Helper()

	memServer := imapmemserver.New()
	user := imapmemserver.NewUser(testIMAPUser, testIMAPPassword)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatalf("Create(INBOX) = %v", err)
	}
	memServer.AddUser(user)

	var (
		mu       sync.Mutex
		captured []*xoauth2MemSession
	)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(_ *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			s := &xoauth2MemSession{
				Session:         memServer.NewSession(),
				expectedUser:    testIMAPUser,
				postAuthLoginPw: testIMAPPassword,
			}
			mu.Lock()
			captured = append(captured, s)
			mu.Unlock()
			return s, nil, nil
		},
		InsecureAuth: true,
		Caps: imap.CapSet{
			imap.CapIMAP4rev1:         {},
			imap.CapSASLIR:            {},
			imap.AuthCap(xoauth2Mech): {},
		},
		Logger: discardServerLogger{},
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() = %v", err)
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.Serve(ln)
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-served
		_ = srv.Close()
	})

	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port, &captured
}

func TestImapProvider_OAuth2HappyPath(t *testing.T) {
	t.Parallel()

	host, port, sessions := startXOAUTH2MemIMAPServer(t)

	const expectedToken = "tok-abc-123"
	ts := &fakeTokenSource{token: expectedToken}

	provider := &ImapProvider{
		Host:        host,
		Port:        port,
		Mailbox:     "INBOX",
		Username:    testIMAPUser,
		UseOAuth2:   true,
		TokenSource: ts,
		Insecure:    true,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := provider.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = provider.Disconnect(context.Background()) })

	if !provider.IsConnected() {
		t.Error("IsConnected() = false after OAuth2 Connect")
	}
	if provider.LastSuccessfulAuthAt().IsZero() {
		t.Error("LastSuccessfulAuthAt is zero after successful OAuth2 Connect")
	}
	if got := ts.Calls(); got == 0 {
		t.Error("TokenSource.Token() was never called; provider did not request a bearer token")
	}

	if len(*sessions) == 0 {
		t.Fatal("no SASL sessions recorded on server side")
	}
	last := (*sessions)[len(*sessions)-1]
	last.mu.Lock()
	verified := last.verifiedToken
	last.mu.Unlock()
	if verified == nil {
		t.Fatal("server side verifiedToken is nil; XOAUTH2 verify did not run")
	}
	if *verified != expectedToken {
		t.Errorf("server received token %q, want %q", *verified, expectedToken)
	}
}

func TestImapProvider_OAuth2TokenFetchFails(t *testing.T) {
	t.Parallel()

	host, port, _ := startXOAUTH2MemIMAPServer(t)

	ts := &fakeTokenSource{err: errors.New("invalid_client")}

	provider := &ImapProvider{
		Host:        host,
		Port:        port,
		Mailbox:     "INBOX",
		Username:    testIMAPUser,
		UseOAuth2:   true,
		TokenSource: ts,
		Insecure:    true,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := provider.Connect(context.Background())
	if err == nil {
		t.Fatal("Connect() with failing TokenSource = nil; want error")
	}
	if !strings.Contains(err.Error(), "oauth2") {
		t.Errorf("error %q does not mention oauth2 source of failure", err.Error())
	}
	if !strings.Contains(err.Error(), "invalid_client") {
		t.Errorf("error %q does not preserve underlying token error", err.Error())
	}
	if provider.IsConnected() {
		t.Error("IsConnected() = true after a failed OAuth2 Connect")
	}
}

func TestImapProvider_OAuth2MissingTokenSource(t *testing.T) {
	t.Parallel()

	provider := &ImapProvider{
		Host:      "127.0.0.1",
		Port:      993,
		Mailbox:   "INBOX",
		Username:  testIMAPUser,
		UseOAuth2: true,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := provider.Connect(context.Background())
	if err == nil {
		t.Fatal("Connect() with UseOAuth2=true and no TokenSource = nil; want error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "oauth2") {
		t.Errorf("error %q does not mention oauth2 misconfiguration", err.Error())
	}
}
