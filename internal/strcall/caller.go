// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package strcall

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

const tokenTTL = 15 * time.Minute

type File struct {
	Name     string
	MIMEType string
	Data     []byte
}

type Call struct {
	Name           string
	Args           map[string]any
	ToolConfig     map[string]any
	Files          map[string]File
	TimeoutSeconds int
	Spend          *remotetool.SpendAttribution
	TraceID        string
}

type Caller struct {
	exec    *remote.Executor
	signer  *trust.Signer
	subject string
}

func New(brk broker.Broker, namespace string, signer *trust.Signer, subject string, logger *slog.Logger) (*Caller, error) {
	if !rbac.IsReservedSystemUserSub(subject) {
		return nil, fmt.Errorf("%q is not a system user", subject)
	}
	name := "platform-" + idgen.NewRandom()
	return &Caller{exec: remote.NewExecutor(brk, namespace, name, remote.WithLogger(logger)), signer: signer, subject: subject}, nil
}

func (s *Caller) Do(ctx context.Context, c Call) (map[string]any, error) {
	if err := s.exec.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("start tool call replies: %w", err)
	}
	corrID := idgen.New()
	traceID := c.TraceID
	if traceID == "" {
		traceID = corrID
	}
	token := ""
	if s.signer != nil {
		var err error
		token, err = s.signer.Sign(trust.SignClaims{Subject: s.subject, TaskID: corrID, TTL: tokenTTL})
		if err != nil {
			return nil, fmt.Errorf("sign tool call: %w", err)
		}
	}
	preloaded := make(map[string]any, len(c.Files))
	for param, f := range c.Files {
		preloaded[param] = map[string]any{"filename": f.Name, "content": base64.StdEncoding.EncodeToString(f.Data), "mime_type": f.MIMEType}
	}
	resp, err := s.exec.Invoke(ctx, corrID, &remote.InvokeRequest{
		ToolName: c.Name, Args: c.Args, ToolConfig: c.ToolConfig, AppName: "platform", UserID: s.subject,
		PreloadedArtifacts: preloaded, TimeoutSeconds: c.TimeoutSeconds, AuthToken: token, TraceID: traceID, Spend: c.Spend,
	}, nil)
	if err != nil {
		return nil, err
	}
	return resp.Result.ToolResult, nil
}

func (s *Caller) Close() error {
	s.exec.Stop()
	return nil
}
