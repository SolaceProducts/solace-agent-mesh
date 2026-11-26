// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"strings"
	"testing"
)

const anyProblem = "any"

func TestNormalizeDomain(t *testing.T) {
	for name, tc := range map[string]struct {
		in          string
		wantDomain  string
		wantProblem string
	}{
		"plain":          {in: "example.com", wantDomain: "example.com"},
		"trim and case":  {in: " Example.COM ", wantDomain: "example.com"},
		"leading at":     {in: "@example.com", wantDomain: "example.com"},
		"whole address":  {in: "someone@example.com", wantProblem: "enter the domain only"},
		"two ats":        {in: "a@b@c.com", wantProblem: "enter the domain only"},
		"space after at": {in: "@ example.com", wantDomain: "example.com"},
		"underscore":     {in: "my_corp.com", wantProblem: anyProblem},
		"leading dot":    {in: ".corp.com", wantProblem: anyProblem},
		"trailing dot":   {in: "corp.com.", wantProblem: anyProblem},
		"nested labels":  {in: "eng.example.co.uk", wantDomain: "eng.example.co.uk"},
		"empty":          {in: ""},
		"no dot":         {in: "gmail", wantProblem: "gmail.com"},
		"leading hyphen": {in: "-bad.com", wantProblem: anyProblem},
		"space inside":   {in: "a b.com", wantProblem: anyProblem},
		"path":           {in: "example.com/x", wantProblem: anyProblem},
	} {
		t.Run(name, func(t *testing.T) {
			domain, problem := NormalizeDomain(tc.in)
			if domain != tc.wantDomain {
				t.Errorf("domain = %q, want %q", domain, tc.wantDomain)
			}
			switch tc.wantProblem {
			case "":
				if problem != "" {
					t.Errorf("problem = %q, want none", problem)
				}
			case anyProblem:
				if problem == "" {
					t.Error("want a problem")
				}
			default:
				if !strings.Contains(problem, tc.wantProblem) {
					t.Errorf("problem = %q, want it to contain %q", problem, tc.wantProblem)
				}
			}
		})
	}
}
