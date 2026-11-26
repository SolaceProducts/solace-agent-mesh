// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
)

type PortAvailabilityCheck struct {
	PIDLookup func(port int) string
}

func NewPortAvailabilityCheck() *PortAvailabilityCheck { return &PortAvailabilityCheck{} }

func (PortAvailabilityCheck) Name() string { return "port_availability" }

func (PortAvailabilityCheck) DisplayName() string { return "Port Availability" }

func (c *PortAvailabilityCheck) Run(_ context.Context, env Env, _ Context) CheckResult {
	ports := []int{
		portValueOr(env.FastAPIPort, 8800),
		portValueOr(env.PlatformAPIPort, 8001),
	}

	pidLookup := c.PIDLookup
	if pidLookup == nil {
		pidLookup = lsofPIDInfo
	}

	var failing []string
	var failingPorts []int
	for _, p := range ports {
		ln, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", p))
		if err != nil {
			pidInfo := pidLookup(p)
			failing = append(failing, fmt.Sprintf("port %d in use%s", p, pidInfo))
			failingPorts = append(failingPorts, p)
			continue
		}
		_ = ln.Close()
	}

	if len(failing) > 0 {
		detailParts := make([]string, 0, len(failingPorts))
		for _, p := range failingPorts {
			detailParts = append(detailParts, strconv.Itoa(p))
		}
		return CheckResult{
			Name:   c.DisplayName(),
			Status: StatusFail,
			Detail: strings.Join(detailParts, ", "),
			Reason: strings.Join(failing, "; "),
		}
	}
	parts := make([]string, len(ports))
	for i, p := range ports {
		parts[i] = strconv.Itoa(p)
	}
	return CheckResult{Name: c.DisplayName(), Status: StatusPass, Detail: strings.Join(parts, ", ")}
}

func portValueOr(raw string, def int) int {
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return v
}

func lsofPIDInfo(port int) string {
	out, err := exec.Command("lsof", "-nP", "-i", fmt.Sprintf(":%d", port), "-sTCP:LISTEN", "-Fpc").Output()
	if err != nil {
		return ""
	}
	var pid, name string
	for line := range strings.SplitSeq(string(out), "\n") {
		if len(line) == 0 {
			continue
		}
		switch line[0] {
		case 'p':
			pid = line[1:]
		case 'c':
			name = line[1:]
		}
	}
	if pid == "" {
		return ""
	}
	if name == "" {
		return fmt.Sprintf(" (PID %s)", pid)
	}
	return fmt.Sprintf(" (PID %s: %s)", pid, name)
}
