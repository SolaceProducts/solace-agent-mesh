// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const defaultDatabasePort = 5432

type dbVar struct {
	label    string
	required bool
	get      func(env Env) string
}

var databaseVars = []dbVar{
	{"platform service", true, func(e Env) string { return e.PlatformDatabaseURL }},
	{"orchestrator agent", false, func(e Env) string { return e.OrchestratorDatabaseURL }},
	{"web UI gateway", false, func(e Env) string { return e.WebUIGatewayDatabaseURL }},
}

type dbItem struct {
	label   string
	status  Status
	message string
}

type DatabaseTCPCheck struct {
	Dialer func(ctx context.Context, addr string) error
}

func NewDatabaseTCPCheck() *DatabaseTCPCheck { return &DatabaseTCPCheck{} }

func (DatabaseTCPCheck) Name() string { return "database_reachable" }

func (DatabaseTCPCheck) DisplayName() string { return "Database Reachable" }

func (c *DatabaseTCPCheck) Run(ctx context.Context, env Env, doctorCtx Context) CheckResult {
	dialer := c.Dialer
	if dialer == nil {
		dialer = func(ctx context.Context, addr string) error {
			d := net.Dialer{Timeout: 5 * time.Second}
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				return err
			}
			return conn.Close()
		}
	}

	var items []dbItem
	for _, v := range databaseVars {
		url := v.get(env)
		if url == "" || isSQLite(url) {
			it, ok := missingItem(v, url, doctorCtx)
			if ok {
				items = append(items, it)
			}
			continue
		}
		host, port, err := parseDatabaseURL(url)
		if err != nil {
			items = append(items, dbItem{v.label, StatusFail, fmt.Sprintf("could not parse URL: %v", err)})
			continue
		}
		addr := fmt.Sprintf("%s:%d", host, port)
		if err := dialer(ctx, addr); err != nil {
			items = append(items, dbItem{v.label, StatusFail, fmt.Sprintf("TCP connection failed: %v", err)})
			continue
		}
		items = append(items, dbItem{v.label, StatusPass, addr})
	}
	return aggregateDatabase(c.DisplayName(), items)
}

type DatabaseAuthCheck struct {
	ConnectFunc func(ctx context.Context, dsn string) error
}

func NewDatabaseAuthCheck() *DatabaseAuthCheck { return &DatabaseAuthCheck{} }

func (DatabaseAuthCheck) Name() string { return "database_authentication" }

func (DatabaseAuthCheck) DisplayName() string { return "Database Authentication" }

func (c *DatabaseAuthCheck) Run(ctx context.Context, env Env, doctorCtx Context) CheckResult {
	connectFn := c.ConnectFunc
	if connectFn == nil {
		connectFn = pgxConnect
	}

	var items []dbItem
	for _, v := range databaseVars {
		url := v.get(env)
		if url == "" || isSQLite(url) {
			it, ok := missingItem(v, url, doctorCtx)
			if ok {
				items = append(items, it)
			}
			continue
		}
		host, port, err := parseDatabaseURL(url)
		if err != nil {
			items = append(items, dbItem{v.label, StatusFail, fmt.Sprintf("could not parse URL: %v", err)})
			continue
		}
		addr := fmt.Sprintf("%s:%d", host, port)
		dsn := stripDriverSuffix(url)
		connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := connectFn(connectCtx, dsn); err != nil {
			cancel()
			items = append(items, dbItem{v.label, StatusWarn, fmt.Sprintf("cannot verify database authentication: %s", firstLine(err.Error(), 200))})
			continue
		}
		cancel()
		items = append(items, dbItem{v.label, StatusPass, addr})
	}
	return aggregateDatabase(c.DisplayName(), items)
}

func pgxConnect(ctx context.Context, dsn string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	return conn.Close(ctx)
}

func isSQLite(url string) bool {
	return strings.HasPrefix(strings.ToLower(url), "sqlite")
}

func missingItem(v dbVar, raw string, doctorCtx Context) (dbItem, bool) {
	status := missingStatus(doctorCtx, v.required)
	if status == StatusSkip {
		return dbItem{}, false
	}
	reason := raw == ""
	msg := "SQLite not suitable for production"
	if reason {
		msg = fmt.Sprintf("%s not set", databaseEnvName(v))
	}
	return dbItem{v.label, status, msg}, true
}

func databaseEnvName(v dbVar) string {
	switch v.label {
	case "platform service":
		return "PLATFORM_DATABASE_URL"
	case "orchestrator agent":
		return "ORCHESTRATOR_DATABASE_URL"
	case "web UI gateway":
		return "WEB_UI_GATEWAY_DATABASE_URL"
	}
	return ""
}

func missingStatus(c Context, required bool) Status {
	switch c {
	case ContextHelm:
		return StatusFail
	case ContextWheel:
		if required {
			return StatusFail
		}
		return StatusWarn
	default:
		return StatusSkip
	}
}

func parseDatabaseURL(raw string) (string, int, error) {
	u, err := url.Parse(stripDriverSuffix(raw))
	if err != nil {
		return "", 0, fmt.Errorf("invalid URL syntax")
	}
	host := u.Hostname()
	if host == "" {
		return "", 0, fmt.Errorf("missing host")
	}
	port := defaultDatabasePort
	if p := u.Port(); p != "" {
		v, err := strconv.Atoi(p)
		if err != nil {
			return "", 0, fmt.Errorf("invalid port %q", p)
		}
		port = v
	}
	return host, port, nil
}

func stripDriverSuffix(raw string) string {
	idx := strings.Index(raw, "://")
	if idx <= 0 {
		return raw
	}
	scheme := raw[:idx]
	if i := strings.Index(scheme, "+"); i >= 0 {
		scheme = scheme[:i]
	}
	return scheme + raw[idx:]
}

func aggregateDatabase(name string, items []dbItem) CheckResult {
	if len(items) == 0 {
		return CheckResult{Name: name, Status: StatusSkip, Reason: "No database URLs configured"}
	}

	statuses := make([]Status, len(items))
	for i, it := range items {
		statuses[i] = it.status
	}
	worst := WorstStatus(statuses)

	var failing []dbItem
	for _, it := range items {
		if it.status != StatusPass {
			failing = append(failing, it)
		}
	}

	detail := collapseDatabaseDetail(items)
	if len(failing) == 0 {
		return CheckResult{Name: name, Status: StatusPass, Detail: detail}
	}
	parts := make([]string, len(failing))
	for i, it := range failing {
		parts[i] = fmt.Sprintf("%s [%s]: %s", it.label, it.status, it.message)
	}
	return CheckResult{Name: name, Status: worst, Detail: detail, Reason: strings.Join(parts, "; ")}
}

func collapseDatabaseDetail(items []dbItem) string {
	var passing []dbItem
	for _, it := range items {
		if it.status == StatusPass {
			passing = append(passing, it)
		}
	}
	if len(passing) == 0 {
		return ""
	}
	first := passing[0].message
	allSame := true
	for _, it := range passing[1:] {
		if it.message != first {
			allSame = false
			break
		}
	}
	if allSame {
		suffix := "databases"
		if len(passing) == 1 {
			suffix = "database"
		}
		return fmt.Sprintf("%s — %d %s", first, len(passing), suffix)
	}
	parts := make([]string, len(passing))
	for i, it := range passing {
		parts[i] = fmt.Sprintf("%s: %s", it.label, it.message)
	}
	return strings.Join(parts, ", ")
}
