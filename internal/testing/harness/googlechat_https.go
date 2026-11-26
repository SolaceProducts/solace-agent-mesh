// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"bytes"
	"cmp"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/googlechat"
)

const (
	GoogleChatDeliveryPubSub = googlechat.DeliveryPubSub
	GoogleChatDeliveryHTTPS  = googlechat.DeliveryHTTPS
	GoogleChatDeliveryUnset  = "unset"
)

func (c *GoogleChatTestConfig) validate() error {
	switch c.Delivery {
	case "", GoogleChatDeliveryPubSub, GoogleChatDeliveryHTTPS, GoogleChatDeliveryUnset:
	default:
		return fmt.Errorf("googlechat_config.delivery %q is not one of %q, %q or %q",
			c.Delivery, GoogleChatDeliveryPubSub, GoogleChatDeliveryHTTPS, GoogleChatDeliveryUnset)
	}
	for _, failure := range c.DriveFailures {
		if !slices.Contains(GoogleChatDriveFailures, failure) {
			return fmt.Errorf("googlechat_config.drive_failures %q is not one of %q", failure, GoogleChatDriveFailures)
		}
	}
	if !c.ArtifactDrive && (len(c.DriveFailures) > 0 || c.ArtifactDriveAccessDays != 0 || c.DriveMaxArtifactBytes != 0) {
		return fmt.Errorf("googlechat_config sets a drive option but not artifact_drive: true, so the option would be ignored")
	}
	return nil
}

const (
	GoogleChatTestProjectNumber = "123456789012"
	GoogleChatTestEndpointURL   = "https://sam.example.com/gw/test-gw-googlechat/events"
	googleChatSigner            = "chat@system.gserviceaccount.com"
	googleChatSignerKeyID       = "harness-chat-signer"
	googleChatTokenLifetime     = time.Hour
	googleChatSignerKeyBits     = 2048
)

type googleChatTestSigner struct {
	key     *rsa.PrivateKey
	certPEM string
}

func newGoogleChatTestSigner() (*googleChatTestSigner, error) {
	key, err := rsa.GenerateKey(rand.Reader, googleChatSignerKeyBits)
	if err != nil {
		return nil, fmt.Errorf("generate chat signer key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: googleChatSigner},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create chat signer certificate: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return &googleChatTestSigner{key: key, certPEM: string(certPEM)}, nil
}

func (s *googleChatTestSigner) sign(audience string, expires time.Time) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Issuer:    googleChatSigner,
		Audience:  jwt.ClaimStrings{audience},
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(expires),
	})
	tok.Header["kid"] = googleChatSignerKeyID
	return tok.SignedString(s.key)
}

func (g *GoogleChatTestGateway) overHTTPS() bool {
	return g.cfg.Delivery == GoogleChatDeliveryHTTPS || g.cfg.Delivery == GoogleChatDeliveryUnset
}

func (g *GoogleChatTestGateway) startHTTPSIngress() error {
	signer, err := newGoogleChatTestSigner()
	if err != nil {
		return err
	}
	g.signer = signer
	g.mockServer.PublishSignerCerts(map[string]string{googleChatSignerKeyID: signer.certPEM})
	g.proxy = entrypoint.NewGatewayProxy("127.0.0.1:0", g.logger)
	g.proxyServer = httptest.NewServer(g.proxy.Handler())
	return nil
}

func (g *GoogleChatTestGateway) mount(core *entrypoint.GatewayCore, appConfig map[string]any) error {
	if g.proxy == nil {
		return nil
	}
	exe := entrypoint.NewGatewayExecutor(g.svc, nil)
	exe.AddGateway(g.gatewayID, core, appConfig)
	if err := entrypoint.MountExecutor(g.proxy, exe); err != nil {
		return fmt.Errorf("mount google chat gateway: %w", err)
	}
	return nil
}

func (g *GoogleChatTestGateway) EventsURL() string {
	if g.proxyServer == nil {
		return ""
	}
	return g.proxyServer.URL + "/gw/" + g.gatewayID + "/events"
}

func (g *GoogleChatTestGateway) ChatToken(audience string) (string, error) {
	if g.signer == nil {
		return "", fmt.Errorf("google chat gateway is not running HTTPS delivery")
	}
	return g.signer.sign(audience, time.Now().Add(googleChatTokenLifetime))
}

func (g *GoogleChatTestGateway) ExpiredChatToken() (string, error) {
	if g.signer == nil {
		return "", fmt.Errorf("google chat gateway is not running HTTPS delivery")
	}
	return g.signer.sign(GoogleChatTestProjectNumber, time.Now().Add(-10*time.Minute))
}

func (g *GoogleChatTestGateway) ForgedChatToken() (string, error) {
	forger, err := newGoogleChatTestSigner()
	if err != nil {
		return "", err
	}
	return forger.sign(GoogleChatTestProjectNumber, time.Now().Add(googleChatTokenLifetime))
}

func (g *GoogleChatTestGateway) PostEvent(body []byte, bearer string) (int, error) {
	req, err := http.NewRequest(http.MethodPost, g.EventsURL(), bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build event request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("post event: %w", err)
	}
	if err := resp.Body.Close(); err != nil {
		return resp.StatusCode, fmt.Errorf("close event response: %w", err)
	}
	return resp.StatusCode, nil
}

func (g *GoogleChatTestGateway) postSigned(body []byte) {
	token, err := g.ChatToken(GoogleChatTestProjectNumber)
	if err == nil {
		var status int
		status, err = g.PostEvent(body, token)
		if err == nil && status != http.StatusOK {
			err = fmt.Errorf("events route answered %d", status)
		}
	}
	if err != nil {
		g.mu.Lock()
		g.deliveryErr = cmp.Or(g.deliveryErr, err)
		g.mu.Unlock()
	}
}

func (g *GoogleChatTestGateway) DeliveryErr() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.deliveryErr
}

func (g *GoogleChatTestGateway) stopHTTPSIngress() {
	if g.proxyServer != nil {
		g.proxyServer.Close()
	}
}
