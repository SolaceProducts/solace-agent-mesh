// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type shellPolicy struct {
	off   bool
	allow [][]string
}

func newShellPolicy(noShell bool, allow []string) shellPolicy {
	p := shellPolicy{off: noShell}
	for _, a := range allow {
		if words := strings.Fields(a); len(words) > 0 {
			p.allow = append(p.allow, words)
		}
	}
	return p
}

func (p shellPolicy) restricted() bool { return len(p.allow) > 0 }

func (p shellPolicy) allowedList() string {
	names := make([]string, 0, len(p.allow))
	for _, words := range p.allow {
		names = append(names, strings.Join(words, " "))
	}
	return strings.Join(names, ", ")
}

func (p shellPolicy) note() string {
	switch {
	case p.off:
		return "There is no shell in this run: work with read, grep, glob and the other tools you have, and do not plan on running commands.\n"
	case p.restricted():
		return "bash runs only these commands, as plain words with no shell syntax (no pipes, redirects, ; or &&): " + p.allowedList() + ".\n"
	}
	return ""
}

func (p shellPolicy) tools(specs []sdk.ChatTool) []sdk.ChatTool {
	if !p.off && !p.restricted() {
		return specs
	}
	out := make([]sdk.ChatTool, 0, len(specs))
	for _, t := range specs {
		if t.Name == "bash" {
			if p.off {
				continue
			}
			t.Description += " Only these commands run, without a shell: " + p.allowedList() + ". Output is already combined and capped, so no pipes or redirects are needed."
		}
		out = append(out, t)
	}
	return out
}

func (p shellPolicy) run(ctx context.Context, dir, command string, timeoutSeconds int, env []string) (string, error) {
	if p.off {
		return "", errors.New("there is no shell in this run")
	}
	if !p.restricted() {
		return runShell(ctx, dir, command, timeoutSeconds, env).String(), nil
	}
	argv, err := splitWords(command)
	if err != nil {
		return "", err
	}
	if !p.allows(argv) {
		return "", fmt.Errorf("%q is not allowed; bash runs only: %s", firstWords(argv, 2), p.allowedList())
	}
	return runArgv(ctx, dir, command, argv, timeoutSeconds, env).String(), nil
}

func (p shellPolicy) allows(argv []string) bool {
	for _, words := range p.allow {
		if len(argv) >= len(words) && equalWords(argv[:len(words)], words) {
			return true
		}
	}
	return false
}

func equalWords(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func firstWords(argv []string, n int) string {
	return strings.Join(argv[:min(n, len(argv))], " ")
}

const shellSyntax = ";&|<>`$()\n\r\\"

func splitWords(command string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	var quote rune
	for _, r := range command {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			if quote == '"' && (r == '$' || r == '`' || r == '\\') {
				return nil, fmt.Errorf("shell syntax %q is not supported here; pass plain arguments", r)
			}
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case strings.ContainsRune(shellSyntax, r):
			return nil, fmt.Errorf("shell syntax %q is not supported here: run one command with plain arguments; its output is already combined and capped", r)
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	if inWord {
		words = append(words, cur.String())
	}
	if len(words) == 0 {
		return nil, errors.New("empty command")
	}
	return words, nil
}
