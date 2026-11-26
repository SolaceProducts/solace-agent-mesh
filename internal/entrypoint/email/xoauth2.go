// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"errors"
	"fmt"

	"github.com/emersion/go-sasl"
)

const xoauth2Mech = "XOAUTH2"

type xoauth2Client struct {
	user  string
	token string
}

func newXoauth2Client(user, token string) sasl.Client {
	return &xoauth2Client{user: user, token: token}
}

func (c *xoauth2Client) Start() (mech string, ir []byte, err error) {
	ir = []byte(fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", c.user, c.token))
	return xoauth2Mech, ir, nil
}

func (c *xoauth2Client) Next(_ []byte) ([]byte, error) {
	return nil, fmt.Errorf("xoauth2: server rejected token")
}

type xoauth2Server struct {
	verify func(user, token string) error
	done   bool
}

func newXoauth2Server(verify func(user, token string) error) sasl.Server {
	return &xoauth2Server{verify: verify}
}

func (s *xoauth2Server) Next(response []byte) (challenge []byte, done bool, err error) {
	if s.done {
		return nil, true, errors.New("xoauth2: unexpected client response after completion")
	}
	if response == nil {
		return []byte{}, false, nil
	}
	user, token, err := parseXoauth2Blob(response)
	if err != nil {
		s.done = true
		return nil, true, err
	}
	if err := s.verify(user, token); err != nil {
		s.done = true
		return nil, true, err
	}
	s.done = true
	return nil, true, nil
}

func parseXoauth2Blob(b []byte) (user, token string, err error) {
	const userPrefix = "user="
	const authPrefix = "auth=Bearer "

	s := string(b)
	for len(s) > 0 && s[len(s)-1] == 0x01 {
		s = s[:len(s)-1]
	}

	for _, segment := range splitByCtrlA(s) {
		switch {
		case len(segment) >= len(userPrefix) && segment[:len(userPrefix)] == userPrefix:
			user = segment[len(userPrefix):]
		case len(segment) >= len(authPrefix) && segment[:len(authPrefix)] == authPrefix:
			token = segment[len(authPrefix):]
		}
	}
	if user == "" || token == "" {
		return "", "", fmt.Errorf("xoauth2: malformed credential blob")
	}
	return user, token, nil
}

func splitByCtrlA(s string) []string {
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == 0x01 {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
