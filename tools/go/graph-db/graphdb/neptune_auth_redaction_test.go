// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package graphdb

import (
	"strings"
	"testing"
)

func TestNeptuneEndpointFromConnStr_ErrorsCarryNoCredentials(t *testing.T) {
	const password = "hunter2"
	cases := map[string]string{
		"unparseable port": "bolt://svc:" + password + "@graph-db:notaport",
		"no host":          "bolt://",
	}
	exercised := 0
	for name, connStr := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := neptuneEndpointFromConnStr(connStr)
			if err == nil {
				t.Skipf("%q parsed successfully; this case no longer exercises an error path", connStr)
			}
			exercised++
			if strings.Contains(err.Error(), password) {
				t.Errorf("error carries the password: %q", err)
			}
			if strings.Contains(err.Error(), connStr) {
				t.Errorf("error carries the whole connection string: %q", err)
			}
			if err.Error() == "" {
				t.Error("error message is empty — the diagnostic was removed rather than sanitized")
			}
		})
	}
	if exercised == 0 {
		t.Fatal("every case parsed successfully, so nothing was asserted")
	}
}

func TestNeo4jConnect_ErrorsCarryNoConnectionString(t *testing.T) {
	const password = "hunter2"
	cases := map[string]string{
		"unparseable port": "bolt://svc:" + password + "@graph-db:notaport",
		"bad escape":       "bolt://svc:" + password + "@graph-db%zz:7687",
		"unmatched [":      "bolt://svc:" + password + "@[graph-db:7687",
	}
	for name, connStr := range cases {
		t.Run(name, func(t *testing.T) {
			backend := newNeo4jBackend(map[string]any{
				"connection_string": connStr,
				"username":          "svc",
				"password":          password,
			})
			err := backend.Connect(t.Context())
			if err == nil {
				t.Fatalf("%q connected successfully; this case no longer exercises an error path", connStr)
			}
			msg := err.Error()
			if strings.Contains(msg, password) {
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
}
