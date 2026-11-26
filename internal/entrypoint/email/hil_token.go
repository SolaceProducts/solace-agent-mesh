// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/hmacutil"
)

type hilToken struct {
	RequestID string `json:"r"`
	TaskID    string `json:"t"`
	AgentName string `json:"a"`
	UserID    string `json:"u"`
	ExpiresAt int64  `json:"e"`
}

var (
	errTokenInvalid = errors.New("invalid HIL token")
	errTokenExpired = errors.New("HIL token expired")
)

func signHILToken(tok hilToken, secret []byte) (string, error) {
	payload, err := json.Marshal(tok)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	sig := hmacutil.Sum(secret, payload)
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(sig), nil
}

func verifyHILToken(s string, secret []byte) (hilToken, error) {
	var tok hilToken
	dot := strings.IndexByte(s, '.')
	if dot <= 0 || dot == len(s)-1 {
		return tok, errTokenInvalid
	}
	enc := base64.RawURLEncoding
	payload, err := enc.DecodeString(s[:dot])
	if err != nil {
		return tok, errTokenInvalid
	}
	sig, err := enc.DecodeString(s[dot+1:])
	if err != nil {
		return tok, errTokenInvalid
	}
	if !hmacutil.Valid(secret, payload, sig) {
		return tok, errTokenInvalid
	}
	if err := json.Unmarshal(payload, &tok); err != nil {
		return tok, errTokenInvalid
	}
	if tok.ExpiresAt > 0 && time.Now().Unix() > tok.ExpiresAt {
		return tok, errTokenExpired
	}
	return tok, nil
}
