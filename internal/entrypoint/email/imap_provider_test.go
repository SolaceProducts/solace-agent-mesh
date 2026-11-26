// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

type discardServerLogger struct{}

func (discardServerLogger) Printf(_ string, _ ...any) {}

func newSeederClient(t *testing.T, conn net.Conn) *imapclient.Client {
	t.Helper()
	client := imapclient.New(conn, nil)
	if err := client.Login(testIMAPUser, testIMAPPassword).Wait(); err != nil {
		t.Fatalf("seed login: %v", err)
	}
	return client
}

const (
	testIMAPUser     = "alice@example.com"
	testIMAPPassword = "app-password"
)

func startMemIMAPServer(t *testing.T) (host string, port int) {
	t.Helper()

	memServer := imapmemserver.New()
	user := imapmemserver.NewUser(testIMAPUser, testIMAPPassword)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatalf("Create(INBOX) = %v", err)
	}
	memServer.AddUser(user)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(_ *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memServer.NewSession(), nil, nil
		},
		InsecureAuth: true,
		Caps: imap.CapSet{
			imap.CapIMAP4rev1: {},
			imap.CapIMAP4rev2: {},
		},
		Logger: discardServerLogger{},
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() = %v", err)
	}

	go func() {
		_ = srv.Serve(ln)
	}()

	t.Cleanup(func() {
		_ = srv.Close()
	})

	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

func seedINBOX(t *testing.T, host string, port int, messages [][]byte) {
	t.Helper()

	addr := host + ":" + itoa(port)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("seed dial: %v", err)
	}
	client := newSeederClient(t, conn)
	defer func() { _ = client.Close() }()

	for _, raw := range messages {
		appendCmd := client.Append("INBOX", int64(len(raw)), nil)
		if _, err := appendCmd.Write(raw); err != nil {
			t.Fatalf("append write: %v", err)
		}
		if err := appendCmd.Close(); err != nil {
			t.Fatalf("append close: %v", err)
		}
		if _, err := appendCmd.Wait(); err != nil {
			t.Fatalf("append wait: %v", err)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [10]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestImapProvider_BasicAuthHappyPath(t *testing.T) {
	t.Parallel()

	host, port := startMemIMAPServer(t)

	rfc822, err := os.ReadFile("testdata/eml/synthetic/basic_text.eml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	seedINBOX(t, host, port, [][]byte{rfc822})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	provider := &ImapProvider{
		Host:     host,
		Port:     port,
		Mailbox:  "INBOX",
		Username: testIMAPUser,
		Password: testIMAPPassword,
		Insecure: true,
		Logger:   logger,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if connErr := provider.Connect(ctx); connErr != nil {
		t.Fatalf("Connect: %v", connErr)
	}
	defer func() { _ = provider.Disconnect(context.Background()) }()

	if !provider.IsConnected() {
		t.Error("IsConnected() = false after Connect")
	}
	if provider.LastSuccessfulAuthAt().IsZero() {
		t.Error("LastSuccessfulAuthAt is zero after successful Connect")
	}
	if !provider.SupportsIdle() {
		t.Error("SupportsIdle() = false; expected true (imapmemserver advertises IMAP4rev2 → IDLE implied)")
	}

	msgs, err := provider.FetchNew(ctx, 10)
	if err != nil {
		t.Fatalf("FetchNew: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("FetchNew returned %d messages; want 1", len(msgs))
	}
	got := msgs[0]
	if got.UID == "" {
		t.Error("UID is empty")
	}
	if !strings.Contains(string(got.RFC822), "Order request") {
		t.Errorf("RFC822 missing seeded subject; got %q", string(got.RFC822))
	}
	if got.FetchedAt.IsZero() {
		t.Error("FetchedAt is zero")
	}

	if markErr := provider.MarkProcessed(ctx, got.UID); markErr != nil {
		t.Fatalf("MarkProcessed: %v", markErr)
	}
	msgs2, err := provider.FetchNew(ctx, 10)
	if err != nil {
		t.Fatalf("FetchNew (after mark): %v", err)
	}
	if len(msgs2) != 0 {
		t.Errorf("FetchNew (after mark) returned %d messages; want 0", len(msgs2))
	}

	if err := provider.Disconnect(ctx); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if provider.IsConnected() {
		t.Error("IsConnected() = true after Disconnect")
	}
}

func TestImapProvider_SupportsIdle_ReportsServerCapability(t *testing.T) {
	t.Parallel()

	host, port := startMemIMAPServer(t)

	provider := &ImapProvider{
		Host:     host,
		Port:     port,
		Mailbox:  "INBOX",
		Username: testIMAPUser,
		Password: testIMAPPassword,
		Insecure: true,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if provider.SupportsIdle() {
		t.Error("SupportsIdle() = true before Connect; expected false")
	}

	if err := provider.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = provider.Disconnect(context.Background()) })

	if !provider.SupportsIdle() {
		t.Error("SupportsIdle() = false after Connect to imapmemserver; expected true (IDLE is advertised)")
	}

	if err := provider.Disconnect(t.Context()); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if provider.SupportsIdle() {
		t.Error("SupportsIdle() = true after Disconnect; expected false")
	}
}

func TestImapProvider_Idle_DeliversExistsOnArrival(t *testing.T) {
	t.Parallel()

	host, port := startMemIMAPServer(t)

	provider := &ImapProvider{
		Host:     host,
		Port:     port,
		Mailbox:  "INBOX",
		Username: testIMAPUser,
		Password: testIMAPPassword,
		Insecure: true,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if err := provider.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = provider.Disconnect(context.Background()) })

	events, _, err := provider.Idle(t.Context())
	if err != nil {
		t.Fatalf("Idle: %v", err)
	}

	rfc822, err := os.ReadFile("testdata/eml/synthetic/basic_text.eml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	seedINBOX(t, host, port, [][]byte{rfc822})

	var got IdleEvent
	received := false
	deadline := time.Now().Add(10 * time.Second)
	for !received {
		select {
		case got = <-events:
			received = true
		case <-time.After(200 * time.Millisecond):
			if time.Now().After(deadline) {
				t.Fatal("no IDLE event delivered after message arrival")
			}
			seedINBOX(t, host, port, [][]byte{rfc822})
		}
	}
	if got.Kind != IdleEventExists {
		t.Errorf("got event kind %v, want IdleEventExists", got.Kind)
	}

	if stopErr := provider.StopIdle(t.Context()); stopErr != nil {
		t.Fatalf("StopIdle: %v", stopErr)
	}

	msgs, err := provider.FetchNew(t.Context(), 10)
	if err != nil {
		t.Fatalf("FetchNew after StopIdle: %v", err)
	}
	if len(msgs) < 1 {
		t.Errorf("FetchNew returned %d messages, want >= 1", len(msgs))
	}
}

func TestImapProvider_FetchNewNotConnected(t *testing.T) {
	t.Parallel()
	provider := &ImapProvider{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if _, err := provider.FetchNew(context.Background(), 10); err == nil {
		t.Fatal("FetchNew on disconnected provider returned nil error")
	}
}
