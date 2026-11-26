// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

const defaultBrokerPort = 55555

type BrokerCheck struct {
	BrokerFactory func(logger *slog.Logger, clientName string) (broker.Broker, error)
}

func NewBrokerCheck() *BrokerCheck { return &BrokerCheck{} }

func (BrokerCheck) Name() string { return "broker_connectivity" }

func (BrokerCheck) DisplayName() string { return "Broker Connectivity" }

func (c *BrokerCheck) Run(ctx context.Context, env Env, doctorCtx Context) CheckResult {
	if doctorCtx == ContextLocal && strings.EqualFold(env.SolaceDevMode, "true") {
		return CheckResult{
			Name:   c.DisplayName(),
			Status: StatusSkip,
			Reason: "SOLACE_DEV_MODE=true — broker not used in local context",
		}
	}

	if env.SolaceBrokerURL == "" {
		status := StatusSkip
		if doctorCtx.IsStrict() {
			status = StatusFail
		}
		return CheckResult{Name: c.DisplayName(), Status: status, Reason: "SOLACE_BROKER_URL not set"}
	}

	host, port, err := parseBrokerURL(env.SolaceBrokerURL)
	if err != nil {
		return CheckResult{
			Name:   c.DisplayName(),
			Status: StatusFail,
			Detail: env.SolaceBrokerURL,
			Reason: fmt.Sprintf("Could not parse SOLACE_BROKER_URL: %v", err),
		}
	}
	detail := fmt.Sprintf("%s:%d", host, port)

	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", detail)
	if err != nil {
		return CheckResult{
			Name:   c.DisplayName(),
			Status: StatusFail,
			Detail: detail,
			Reason: fmt.Sprintf("TCP connection failed: %v", err),
		}
	}
	_ = conn.Close()

	if env.SolaceBrokerVPN != "" {
		_ = setEnv("SOLACE_BROKER_VPN", env.SolaceBrokerVPN)
	}
	if env.SolaceBrokerUsername != "" {
		_ = setEnv("SOLACE_BROKER_USERNAME", env.SolaceBrokerUsername)
	}
	if env.SolaceBrokerPassword != "" {
		_ = setEnv("SOLACE_BROKER_PASSWORD", env.SolaceBrokerPassword)
	}
	_ = setEnv("SOLACE_BROKER_URL", env.SolaceBrokerURL)

	factory := c.BrokerFactory
	if factory == nil {
		factory = samapp.CreateBroker
	}
	logger := slog.New(slog.DiscardHandler)
	br, err := factory(logger, "sam-doctor")
	if err != nil {
		return CheckResult{
			Name:   c.DisplayName(),
			Status: StatusFail,
			Detail: detail,
			Reason: fmt.Sprintf("Broker authentication failed: %s", firstLine(err.Error(), 200)),
		}
	}
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := br.Connect(connectCtx); err != nil {
		_ = br.Disconnect(ctx)
		return CheckResult{
			Name:   c.DisplayName(),
			Status: StatusFail,
			Detail: detail,
			Reason: fmt.Sprintf("Broker authentication failed: %s", firstLine(err.Error(), 200)),
		}
	}
	_ = br.Disconnect(ctx)
	return CheckResult{Name: c.DisplayName(), Status: StatusPass, Detail: detail}
}

func parseBrokerURL(raw string) (string, int, error) {
	s := raw
	if !strings.Contains(s, "://") {
		s = "tcp://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", 0, err
	}
	host := u.Hostname()
	if host == "" {
		return "", 0, fmt.Errorf("missing host")
	}
	port := defaultBrokerPort
	if p := u.Port(); p != "" {
		v, err := strconv.Atoi(p)
		if err != nil {
			return "", 0, fmt.Errorf("invalid port %q", p)
		}
		port = v
	}
	return host, port, nil
}

func firstLine(s string, maxLen int) string {
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}

var setEnv = func(key, value string) error {
	return osSetenv(key, value)
}
