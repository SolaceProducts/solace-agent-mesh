// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

const maxCertFileBytes = 1 << 20

func RegisterInstanceUp(reg *Registry, componentType string) {
	if reg == nil {
		return
	}
	reg.ObservableConst("instance.up", 1, "component_type", componentType)
	RegisterGoRuntimeMetrics(reg)
}

func RegisterComponentCount(reg *Registry, kind string, names func() []string) {
	if reg == nil || names == nil {
		return
	}
	reg.ObservableGauge("component.count", func(context.Context) []Observation {
		current := names()
		obs := make([]Observation, 0, len(current))
		for _, n := range current {
			obs = append(obs, NewObservation(1, "type", kind, "name", n))
		}
		return obs
	})
}

func RegisterGatewayInFlightTasks(reg *Registry, count func() int) {
	if reg == nil || count == nil {
		return
	}
	reg.ObservableValue("gateway.tasks.in_flight", func() float64 {
		return float64(count())
	})
}

type ActiveTaskCount struct {
	Kind    string
	Trigger string
	Count   int
}

func RegisterActiveTasks(reg *Registry, componentType string, counts func() []ActiveTaskCount) {
	if reg == nil || counts == nil {
		return
	}
	reg.ObservableGauge("active.tasks", func(context.Context) []Observation {
		current := counts()
		obs := make([]Observation, 0, len(current))
		for _, c := range current {
			obs = append(obs, NewObservation(float64(c.Count),
				"kind", c.Kind,
				"trigger", c.Trigger,
				"component_type", componentType,
			))
		}
		return obs
	})
}

func RegisterAgentActiveExecutions(reg *Registry, counts func() map[string]int) {
	if reg == nil || counts == nil {
		return
	}
	reg.ObservableGauge("agent.active_executions", func(context.Context) []Observation {
		current := counts()
		obs := make([]Observation, 0, len(current))
		for name, n := range current {
			obs = append(obs, NewObservation(float64(n), "agent", name))
		}
		return obs
	})
}

func RegisterCertExpiry(reg *Registry, certFiles ...string) {
	if reg == nil {
		return
	}
	var paths []string
	for _, p := range certFiles {
		if strings.TrimSpace(p) != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return
	}

	logger := reg.logger
	memo := NewTTLMemo(time.Hour, func() ([]*x509.Certificate, error) {
		return loadCerts(paths, logger)
	})

	reg.ObservableGauge("tls.certificate.expiry.days", func(context.Context) []Observation {
		certs, _ := memo.Value()
		now := time.Now()
		obs := make([]Observation, 0, len(certs))
		for _, c := range certs {
			days := c.NotAfter.Sub(now).Hours() / 24
			obs = append(obs, NewObservation(days,
				"common_name", certIdentifier(c),
				"is_ca", strconv.FormatBool(c.IsCA),
			))
		}
		return obs
	})
}

func certIdentifier(c *x509.Certificate) string {
	if cn := strings.TrimSpace(c.Subject.CommonName); cn != "" {
		return cn
	}
	if len(c.DNSNames) > 0 {
		return c.DNSNames[0]
	}
	if len(c.IPAddresses) > 0 {
		return c.IPAddresses[0].String()
	}
	return c.SerialNumber.String()
}

func loadCerts(paths []string, logger *slog.Logger) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	var readErr error
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			logger.Warn("tls cert expiry: cannot open certificate file", "path", p, "error", err)
			readErr = err
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, maxCertFileBytes+1))
		_ = f.Close()
		if err != nil {
			logger.Warn("tls cert expiry: cannot read certificate file", "path", p, "error", err)
			readErr = err
			continue
		}
		if len(data) > maxCertFileBytes {
			logger.Warn("tls cert expiry: certificate file exceeds size cap; skipping (misconfigured ssl_certfile?)",
				"path", p, "cap_bytes", maxCertFileBytes)
			continue
		}
		found := false
		for rest := data; ; {
			var block *pem.Block
			block, rest = pem.Decode(rest)
			if block == nil {
				break
			}
			if block.Type != "CERTIFICATE" {
				continue
			}
			c, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				logger.Warn("tls cert expiry: cannot parse certificate", "path", p, "error", err)
				continue
			}
			certs = append(certs, c)
			found = true
		}
		if !found {
			logger.Warn("tls cert expiry: no certificate in PEM file", "path", p)
		}
	}
	if len(certs) == 0 && readErr != nil {
		return nil, readErr
	}
	return certs, nil
}
