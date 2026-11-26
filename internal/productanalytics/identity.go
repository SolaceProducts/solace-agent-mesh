// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

type Identity struct {
	Marker     string
	BindingKey []byte
}

func (i Identity) bound() bool { return i.Marker != "" && len(i.BindingKey) > 0 }

type IdentityProvider interface {
	Identity(ctx context.Context) Identity
}

type emptyIdentity struct{}

func (emptyIdentity) Identity(context.Context) Identity { return Identity{} }

type IdentityFunc func(ctx context.Context) Identity

func (f IdentityFunc) Identity(ctx context.Context) Identity { return f(ctx) }

const bindingSecretBytes = 32

func newBindingSecret() (string, error) {
	buf := make([]byte, bindingSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mint product analytics binding key: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func validBindingSecret(secret string) bool {
	if len(secret) != hex.EncodedLen(bindingSecretBytes) {
		return false
	}
	_, err := hex.DecodeString(secret)
	return err == nil
}

func identityOf(marker, secret string) Identity {
	if marker == "" || !validBindingSecret(secret) {
		return Identity{}
	}
	return Identity{Marker: marker, BindingKey: []byte(secret)}
}

const (
	IdentityInstall = "install"
	IdentityAccount = "account"
)

type Account struct {
	Subject string
	Email   string
}

type AccountSource func() (Account, bool)

type subject struct {
	Identity
	side    string
	install Identity
	email   string
}

const accountBindingDomain = "productAnalyticsAccountBindingKey"

func subjectOf(install Identity, account Account, accepted bool) subject {
	if !accepted || account.Subject == "" {
		return subject{Identity: install, side: IdentityInstall, install: install}
	}
	mac := hmac.New(sha256.New, install.BindingKey)
	_, _ = mac.Write([]byte(accountBindingDomain))
	return subject{
		Identity: Identity{Marker: account.Subject, BindingKey: mac.Sum(nil)},
		side:     IdentityAccount,
		install:  install,
		email:    account.Email,
	}
}

func emailDomain(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}
