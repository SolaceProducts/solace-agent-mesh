// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"golang.org/x/oauth2/microsoft"
)

var errNotConnected = errors.New("imap: not connected")

type ImapProvider struct {
	Host    string
	Port    int
	Mailbox string

	Username string
	Password string

	OAuth2TenantID     string
	OAuth2ClientID     string
	OAuth2ClientSecret string
	UseOAuth2          bool

	TokenSource oauth2.TokenSource

	Logger *slog.Logger

	Insecure bool

	mu           sync.Mutex
	client       *imapclient.Client
	lastAuthAt   time.Time
	connectedAt  time.Time
	supportsIdle bool

	idleEvents chan IdleEvent

	idleDone chan struct{}

	idleCmd *imapclient.IdleCommand
}

func NewImapProviderBasicAuth(cfg *EmailConfig, logger *slog.Logger) *ImapProvider {
	if logger == nil {
		logger = slog.Default()
	}
	return &ImapProvider{
		Host:     cfg.IMAPHost,
		Port:     cfg.IMAPPort,
		Mailbox:  "INBOX",
		Username: cfg.Mailbox,
		Password: cfg.IMAPPassword,
		Logger:   logger.With("logger_name", "imap-provider", "authMode", "basic"),
	}
}

func NewImapProviderOAuth2(cfg *EmailConfig, logger *slog.Logger) *ImapProvider {
	if logger == nil {
		logger = slog.Default()
	}
	cc := &clientcredentials.Config{
		ClientID:     cfg.OAuth2ClientID,
		ClientSecret: cfg.OAuth2ClientSecret,
		TokenURL:     microsoft.AzureADEndpoint(cfg.OAuth2TenantID).TokenURL,
		Scopes:       []string{"https://outlook.office365.com/.default"},
	}
	return &ImapProvider{
		Host:               cfg.IMAPHost,
		Port:               cfg.IMAPPort,
		Mailbox:            "INBOX",
		Username:           cfg.Mailbox,
		OAuth2TenantID:     cfg.OAuth2TenantID,
		OAuth2ClientID:     cfg.OAuth2ClientID,
		OAuth2ClientSecret: cfg.OAuth2ClientSecret,
		UseOAuth2:          true,
		TokenSource:        cc.TokenSource(context.Background()),
		Logger:             logger.With("logger_name", "imap-provider", "authMode", "oauth2"),
	}
}

func (p *ImapProvider) Connect(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client != nil {
		return nil
	}

	if p.UseOAuth2 && p.TokenSource == nil {
		return errors.New("imap: oauth2 path requires a TokenSource (use NewImapProviderOAuth2)")
	}

	addr := fmt.Sprintf("%s:%d", p.Host, p.Port)

	events := make(chan IdleEvent, 32)
	done := make(chan struct{})
	p.idleEvents = events
	p.idleDone = done

	handler := &imapclient.UnilateralDataHandler{
		Mailbox: func(data *imapclient.UnilateralDataMailbox) {
			if data == nil || data.NumMessages == nil {
				return
			}
			select {
			case <-done:
				return
			default:
			}
			select {
			case events <- IdleEvent{Kind: IdleEventExists}:
			default:
			}
		},
		Expunge: func(_ uint32) {
			select {
			case <-done:
				return
			default:
			}
			select {
			case events <- IdleEvent{Kind: IdleEventExpunge}:
			default:
			}
		},
	}

	opts := &imapclient.Options{
		UnilateralDataHandler: handler,
	}
	c, err := dialIMAP(ctx, addr, p.Host, p.Insecure, p.Logger, opts)
	if err != nil {
		p.idleEvents = nil
		p.idleDone = nil
		return fmt.Errorf("imap dial %s: %w", addr, err)
	}

	if p.UseOAuth2 {
		tok, err := p.TokenSource.Token()
		if err != nil {
			_ = c.Close()
			p.idleEvents = nil
			p.idleDone = nil
			return fmt.Errorf("imap: oauth2 token fetch: %w", err)
		}
		if err := c.Authenticate(newXoauth2Client(p.Username, tok.AccessToken)); err != nil {
			_ = c.Close()
			p.idleEvents = nil
			p.idleDone = nil
			return fmt.Errorf("imap: xoauth2 authenticate: %w", err)
		}
	} else {
		if err := c.Login(p.Username, p.Password).Wait(); err != nil {
			_ = c.Close()
			p.idleEvents = nil
			p.idleDone = nil
			return fmt.Errorf("imap login: %w", err)
		}
	}

	if _, err := c.Select(p.Mailbox, &imap.SelectOptions{ReadOnly: false}).Wait(); err != nil {
		_ = c.Logout().Wait()
		_ = c.Close()
		p.idleEvents = nil
		p.idleDone = nil
		return fmt.Errorf("imap select %s: %w", p.Mailbox, err)
	}

	caps := c.Caps()
	p.supportsIdle = caps.Has(imap.CapIdle)

	now := time.Now()
	p.client = c
	p.lastAuthAt = now
	p.connectedAt = now

	if p.Logger != nil {
		p.Logger.LogAttrs(ctx, slog.LevelInfo, "imap connected",
			slog.String("eventType", "email.imap_connected"),
			slog.String("host", p.Host),
			slog.Int("port", p.Port),
			slog.String("mailbox", p.Mailbox),
		)
	}
	return nil
}

func (p *ImapProvider) Disconnect(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		return nil
	}

	if p.idleCmd != nil {
		_ = p.idleCmd.Close()
		p.idleCmd = nil
	}

	logoutErr := p.client.Logout().Wait()
	closeErr := p.client.Close()
	p.client = nil
	p.supportsIdle = false

	if p.idleDone != nil {
		close(p.idleDone)
	}
	p.idleEvents = nil
	p.idleDone = nil

	if p.Logger != nil {
		p.Logger.LogAttrs(ctx, slog.LevelInfo, "imap disconnected",
			slog.String("eventType", "email.imap_disconnected"),
		)
	}

	switch {
	case logoutErr != nil:
		return fmt.Errorf("imap logout: %w", logoutErr)
	case closeErr != nil:
		return fmt.Errorf("imap close: %w", closeErr)
	}
	return nil
}

func (p *ImapProvider) IsConnected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.client != nil
}

func (p *ImapProvider) LastSuccessfulAuthAt() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastAuthAt
}

func (p *ImapProvider) SupportsIdle() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.supportsIdle
}

func (p *ImapProvider) FetchNew(ctx context.Context, limit int) ([]RawMessage, error) {
	if limit <= 0 {
		return nil, nil
	}

	p.mu.Lock()
	c := p.client
	p.mu.Unlock()
	if c == nil {
		return nil, errNotConnected
	}

	if err := c.Noop().Wait(); err != nil {
		p.invalidateSession(ctx, "noop", err)
		return nil, fmt.Errorf("imap noop: %w", err)
	}

	searchData, err := c.UIDSearch(&imap.SearchCriteria{
		NotFlag: []imap.Flag{imap.FlagSeen},
	}, nil).Wait()
	if err != nil {
		p.invalidateSession(ctx, "uid_search", err)
		return nil, fmt.Errorf("imap uid search: %w", err)
	}
	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		return nil, nil
	}
	if len(uids) > limit {
		uids = uids[:limit]
	}

	bodySection := &imap.FetchItemBodySection{Peek: true}
	fetchOpts := &imap.FetchOptions{
		UID:          true,
		InternalDate: true,
		BodySection:  []*imap.FetchItemBodySection{bodySection},
	}

	bufs, err := c.Fetch(imap.UIDSetNum(uids...), fetchOpts).Collect()
	if err != nil {
		p.invalidateSession(ctx, "uid_fetch", err)
		return nil, fmt.Errorf("imap uid fetch: %w", err)
	}

	now := time.Now()
	out := make([]RawMessage, 0, len(bufs))
	for _, b := range bufs {
		body := b.FindBodySection(bodySection)
		if body == nil {
			if p.Logger != nil {
				p.Logger.LogAttrs(ctx, slog.LevelWarn, "imap fetch missing body",
					slog.String("eventType", "email.imap_fetch_missing_body"),
					slog.Uint64("uid", uint64(b.UID)),
				)
			}
			continue
		}
		out = append(out, RawMessage{
			UID:       ProviderUID(fmt.Sprintf("%d", b.UID)),
			RFC822:    body,
			FetchedAt: now,
		})
	}
	return out, nil
}

func (p *ImapProvider) Idle(ctx context.Context) (<-chan IdleEvent, <-chan struct{}, error) {
	p.mu.Lock()
	c := p.client
	if c == nil {
		p.mu.Unlock()
		return nil, nil, errNotConnected
	}
	if p.idleCmd != nil {
		p.mu.Unlock()
		return nil, nil, errors.New("imap: IDLE already running")
	}
	p.mu.Unlock()

	cmd, err := c.Idle()
	if err != nil {
		p.invalidateSession(ctx, "idle_start", err)
		return nil, nil, fmt.Errorf("imap idle: %w", err)
	}

	p.mu.Lock()
	p.idleCmd = cmd
	events := p.idleEvents
	done := p.idleDone
	p.mu.Unlock()

	if p.Logger != nil {
		p.Logger.LogAttrs(ctx, slog.LevelDebug, "imap idle started",
			slog.String("eventType", "email.imap_idle_started"),
		)
	}
	return events, done, nil
}

func (p *ImapProvider) StopIdle(ctx context.Context) error {
	p.mu.Lock()
	cmd := p.idleCmd
	p.idleCmd = nil
	p.mu.Unlock()

	if cmd == nil {
		return nil
	}

	if err := cmd.Close(); err != nil {
		p.invalidateSession(ctx, "idle_close", err)
		return fmt.Errorf("imap idle close: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		p.invalidateSession(ctx, "idle_wait", err)
		return fmt.Errorf("imap idle wait: %w", err)
	}
	if p.Logger != nil {
		p.Logger.LogAttrs(ctx, slog.LevelDebug, "imap idle stopped",
			slog.String("eventType", "email.imap_idle_stopped"),
		)
	}
	return nil
}

func (p *ImapProvider) MarkProcessed(ctx context.Context, uid ProviderUID) error {
	p.mu.Lock()
	c := p.client
	p.mu.Unlock()
	if c == nil {
		return errNotConnected
	}

	parsed, err := parseUID(uid)
	if err != nil {
		return err
	}

	storeFlags := &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Flags:  []imap.Flag{imap.FlagSeen},
		Silent: true,
	}
	if err := c.Store(imap.UIDSetNum(parsed), storeFlags, nil).Close(); err != nil {
		p.invalidateSession(ctx, "uid_store", err)
		return fmt.Errorf("imap uid store +seen: %w", err)
	}
	return nil
}

func (p *ImapProvider) invalidateSession(ctx context.Context, op string, cause error) {
	p.mu.Lock()
	c := p.client
	p.client = nil
	p.supportsIdle = false
	if p.idleCmd != nil {
		_ = p.idleCmd.Close()
		p.idleCmd = nil
	}
	done := p.idleDone
	p.idleEvents = nil
	p.idleDone = nil
	p.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
	if done != nil {
		close(done)
	}
	if p.Logger != nil {
		p.Logger.LogAttrs(ctx, slog.LevelWarn, "imap session invalidated; will reconnect on next cycle",
			slog.String("eventType", "email.imap_session_invalidated"),
			slog.String("op", op),
			slog.String("error", sanitizeForLog(cause.Error())),
		)
	}
}

func parseUID(uid ProviderUID) (imap.UID, error) {
	n, err := strconv.ParseUint(string(uid), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("imap: invalid provider uid %q: %w", uid, err)
	}
	return imap.UID(n), nil
}

func dialIMAP(ctx context.Context, addr, host string, insecure bool, logger *slog.Logger, opts *imapclient.Options) (*imapclient.Client, error) {
	dialer := &net.Dialer{}
	tcpConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	wrapped := &recoveringConn{Conn: tcpConn, logger: logger}

	if insecure {
		return imapclient.New(wrapped, opts), nil
	}

	tlsConn := tls.Client(wrapped, &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = wrapped.Close()
		return nil, fmt.Errorf("tls handshake: %w", err)
	}
	return imapclient.New(tlsConn, opts), nil
}

var _ EmailProvider = (*ImapProvider)(nil)
