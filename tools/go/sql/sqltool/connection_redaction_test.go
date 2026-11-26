// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sqltool

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const credentialCanary = "hunter2"

func connStrings() map[string]string {
	return map[string]string{
		"unsupported scheme": "mysq://svc:" + credentialCanary + "@prod-db:3306/app",
		"no scheme at all":   "svc:" + credentialCanary + "@prod-db/app",
		"unparseable mysql":  "mysql://svc:" + credentialCanary + "@prod-db:notaport/app",
		"unparseable oracle": "oracle://svc:" + credentialCanary + "@prod-db:notaport/app",
		"empty sqlite path":  "sqlite:///",
	}
}

func TestParseConnectionString_ErrorsCarryNoCredentials(t *testing.T) {
	exercised := 0
	for name, connStr := range connStrings() {
		t.Run(name, func(t *testing.T) {
			_, err := parseConnectionString(connStr)
			if err == nil {
				t.Skipf("%q parsed successfully; this case no longer exercises an error path", connStr)
			}
			exercised++
			msg := err.Error()
			if strings.Contains(msg, credentialCanary) {
				t.Errorf("error carries the password: %q", msg)
			}
			if strings.Contains(msg, connStr) {
				t.Errorf("error carries the whole connection string: %q", msg)
			}
			if msg == "" {
				t.Error("error message is empty — the diagnostic was removed rather than sanitized")
			}
		})
	}
	if exercised == 0 {
		t.Fatal("every case parsed successfully, so nothing was asserted")
	}
}

func TestUnavailableDescription_CarriesNoDetail(t *testing.T) {
	got := unavailableDescription("Query the sales database.")

	if !strings.Contains(got, "UNAVAILABLE") {
		t.Errorf("description no longer tells the model the database is down: %q", got)
	}
	if !strings.Contains(got, "Query the sales database.") {
		t.Errorf("description dropped the authored base text: %q", got)
	}
	for _, leak := range []string{credentialCanary, "svc:", "prod-db", "Connection Error"} {
		if strings.Contains(got, leak) {
			t.Errorf("description contains %q, which is operator detail: %q", leak, got)
		}
	}
	if strings.Contains(strings.ToLower(got), "log") {
		t.Errorf("description points at a log the subprocess path never writes: %q", got)
	}
}

func TestDynamicSchema_BadConnectionStringDoesNotLeak(t *testing.T) {
	for name, connStr := range connStrings() {
		t.Run(name, func(t *testing.T) {
			override := dynamicSchema(map[string]any{
				"connection_string": connStr,
				"tool_description":  "Query the sales database.",
			})
			if override == nil {
				t.Fatal("dynamicSchema returned nil for a non-empty connection string")
			}
			if strings.Contains(override.Description, credentialCanary) {
				t.Errorf("description carries the password: %q", override.Description)
			}
			if strings.Contains(override.Description, connStr) {
				t.Errorf("description carries the whole connection string: %q", override.Description)
			}
			if !strings.Contains(override.Description, "UNAVAILABLE") {
				t.Errorf("description no longer tells the model the database is down: %q", override.Description)
			}
		})
	}
}

func TestDynamicSchema_DriverErrorDetailStaysOutOfDescription(t *testing.T) {
	const connStr = "postgres://svc:" + credentialCanary +
		"@sales-db.internal:5432/orders?sslmode=bogusvalue"

	override := dynamicSchema(map[string]any{
		"connection_string": connStr,
		"tool_description":  "Query the sales database.",
	})
	if override == nil {
		t.Fatal("dynamicSchema returned nil for a non-empty connection string")
	}
	for _, leak := range []string{
		credentialCanary, "svc", "sales-db.internal", "orders", "sslmode",
		"cannot parse", "postgres://",
	} {
		if strings.Contains(override.Description, leak) {
			t.Errorf("description carries driver detail %q: %q", leak, override.Description)
		}
	}
	if !strings.Contains(override.Description, "UNAVAILABLE") {
		t.Errorf("description no longer tells the model the database is down: %q", override.Description)
	}
}

func driverRejectedConnStrings() map[string]string {
	return map[string]string{
		"mssql bad port":    "sqlserver://svc:" + credentialCanary + "@prod-db:notaport?database=app",
		"mssql bad escape":  "sqlserver://svc:" + credentialCanary + "@prod-db%zz:1433?database=app",
		"mssql unmatched [": "sqlserver://svc:" + credentialCanary + "@[prod-db:1433?database=app",
		"mssql alias":       "mssql://svc:" + credentialCanary + "@prod-db:notaport?database=app",
		"postgres libpq pw": "postgres://svc@prod-db:5432/orders?password=" + credentialCanary + "&sslmode=bogusvalue",
	}
}

func TestOpenDB_DriverErrorsCarryNoCredentials(t *testing.T) {
	for name, connStr := range driverRejectedConnStrings() {
		t.Run(name, func(t *testing.T) {
			_, _, err := openDB(connStr)
			if err == nil {
				t.Fatalf("%q opened successfully; this case no longer exercises a driver error", connStr)
			}
			msg := err.Error()
			if strings.Contains(msg, credentialCanary) {
				t.Errorf("error carries the password: %q", msg)
			}
			if strings.Contains(msg, connStr) {
				t.Errorf("error carries the whole connection string: %q", msg)
			}
			if strings.TrimSpace(strings.ReplaceAll(msg, connectionStringOmitted, "")) == "" {
				t.Errorf("nothing but the marker survived, so the operator learns nothing: %q", msg)
			}
		})
	}
}

func TestExecuteQuery_DriverErrorDoesNotReachTheResult(t *testing.T) {
	for name, connStr := range driverRejectedConnStrings() {
		t.Run(name, func(t *testing.T) {
			tc := &sdk.ToolContext{}
			tc.SetConfigForTest(map[string]any{"connection_string": connStr})

			res, err := executeQuery(t.Context(), QueryParams{Query: "SELECT 1"}, tc)
			if err != nil {
				t.Fatalf("executeQuery returned a transport error rather than a Result: %v", err)
			}
			if res == nil {
				t.Fatal("executeQuery returned no Result")
			}
			if strings.Contains(res.Message, credentialCanary) {
				t.Errorf("result carries the password to the model: %q", res.Message)
			}
			if strings.Contains(res.Message, connStr) {
				t.Errorf("result carries the whole connection string to the model: %q", res.Message)
			}
			if res.Message == "" {
				t.Error("result message is empty — the diagnostic was removed rather than sanitized")
			}
		})
	}
}

func TestReportUnavailable_RedactsTheDSNAndKeepsTheReason(t *testing.T) {
	const connStr = "postgres://svc:" + credentialCanary + "@sales-db.internal:5432/orders"

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = saved })

	reportUnavailable("failed to create database engine", connStr,
		fmt.Errorf("ping database: cannot parse `%s`: bad things", connStr))

	if closeErr := w.Close(); closeErr != nil {
		t.Fatalf("close pipe writer: %v", closeErr)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	got := string(out)

	if strings.Contains(got, credentialCanary) {
		t.Errorf("stderr carries the password: %q", got)
	}
	if !strings.Contains(got, "failed to create database engine") {
		t.Errorf("stderr lost the operation: %q", got)
	}
	if !strings.Contains(got, "bad things") {
		t.Errorf("stderr lost the reason, so the operator gained nothing: %q", got)
	}
	if !strings.Contains(got, connectionStringOmitted) {
		t.Errorf("connection string was neither echoed nor marked as dropped: %q", got)
	}
	if strings.Contains(got, "sales-db.internal") {
		t.Errorf("stderr still carries the DSN's host: %q", got)
	}
}

func TestURLScheme_ReportsOnlyARealScheme(t *testing.T) {
	reported := map[string]string{
		"postgres://svc:" + credentialCanary + "@db/app":       "postgres",
		"jdbc:postgresql://svc:" + credentialCanary + "@db/x":  "jdbc:postgresql",
		"postgresql+psycopg2://svc:" + credentialCanary + "@d": "postgresql+psycopg2",
	}
	for connStr, want := range reported {
		if got := urlScheme(connStr); got != want {
			t.Errorf("urlScheme(%q) = %q, want %q", connStr, got, want)
		}
	}

	withheld := []string{
		"Pwd=" + credentialCanary + ";Server=https://prod-db",
		"svc:" + credentialCanary + "@db.internal://orders",
		"admin:" + credentialCanary + "@tcp(db:3306)/x://y",
		"://nothing",
		credentialCanary,
		"svc:" + credentialCanary + "://prod-db",
	}
	for _, connStr := range withheld {
		got := urlScheme(connStr)
		if got != "" {
			t.Errorf("urlScheme(%q) = %q, want \"\" — a non-scheme prefix can carry the credential", connStr, got)
		}
	}
}

func TestOpenDB_EchoedConnectionStringIsDroppedNotMasked(t *testing.T) {
	const connStr = "postgres://svc@prod-db:5432/orders?password=" + credentialCanary +
		"&sslmode=bogusvalue"

	_, _, err := openDB(connStr)
	if err == nil {
		t.Fatal("expected pgx to reject sslmode=bogusvalue")
	}
	msg := err.Error()

	if strings.Contains(msg, credentialCanary) {
		t.Errorf("error carries the password: %q", msg)
	}
	if !strings.Contains(msg, connectionStringOmitted) {
		t.Errorf("pgx echoed the DSN but it was not dropped: %q", msg)
	}
	for _, remnant := range []string{"prod-db", "orders", "password=", "postgres://"} {
		if strings.Contains(msg, remnant) {
			t.Errorf("a masked remnant of the DSN survived (%q): %q", remnant, msg)
		}
	}
	if !strings.Contains(msg, "sslmode is invalid") {
		t.Errorf("the driver's reason did not survive, so the operator learns nothing: %q", msg)
	}
}

func TestOpenDB_PgxBuiltErrorsDropTheDSN(t *testing.T) {
	t.Run("userinfo masked by pgx is still dropped", func(t *testing.T) {
		const connStr = "postgres://svc:" + credentialCanary + "@sales-db.internal:5432/orders?sslmode=bogusvalue"
		_, _, err := openDB(connStr)
		if err == nil {
			t.Fatal("expected pgx to reject sslmode=bogusvalue")
		}
		msg := err.Error()
		if strings.Contains(msg, credentialCanary) {
			t.Errorf("error carries the password: %q", msg)
		}
		for _, leaked := range []string{"sales-db.internal", "orders", "postgres://"} {
			if strings.Contains(msg, leaked) {
				t.Errorf("error still carries %q from the connection string: %q", leaked, msg)
			}
		}
		if !strings.Contains(msg, connectionStringOmitted) {
			t.Errorf("connection string was not dropped: %q", msg)
		}
		if !strings.Contains(msg, "sslmode is invalid") {
			t.Errorf("the reason the operator acts on was lost: %q", msg)
		}
	})

	t.Run("unreachable host carries no credential", func(t *testing.T) {
		_, _, err := openDB("postgres://svc:" + credentialCanary + "@127.0.0.1:1/orders")
		if err == nil {
			t.Fatal("expected a connection failure against a closed port")
		}
		msg := err.Error()
		if strings.Contains(msg, credentialCanary) {
			t.Errorf("error carries the password: %q", msg)
		}
		if !strings.Contains(msg, "connection refused") {
			t.Errorf("the reason the operator acts on was lost: %q", msg)
		}
	})
}
