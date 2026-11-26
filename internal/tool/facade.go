// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

const DefaultUserTimezone = "UTC"

func GetUserTimezone(c *Context) string {
	if c == nil || c.A2AUserConfig == "" {
		return DefaultUserTimezone
	}
	var cfg struct {
		UserTimezone string `json:"user_timezone"`
	}
	if err := json.Unmarshal([]byte(c.A2AUserConfig), &cfg); err != nil {
		return DefaultUserTimezone
	}
	if cfg.UserTimezone == "" {
		return DefaultUserTimezone
	}
	return cfg.UserTimezone
}

func (f *Facade) UserTimezone() string { return GetUserTimezone(f.ctx) }

var ErrNoArtifactService = errors.New("no artifact service configured")

type Facade struct {
	ctx *Context
}

func NewFacade(ctx *Context) *Facade {
	return &Facade{ctx: ctx}
}

func (f *Facade) SessionID() string { return f.ctx.SessionID }

func (f *Facade) UserID() string { return f.ctx.UserID }

func (f *Facade) Caller() auth.CallerIdentity { return f.ctx.Caller() }

func (f *Facade) AgentName() string { return f.ctx.AgentName }

func (f *Facade) Config(key string, defaultVal any) any {
	if f.ctx.ToolConfig == nil {
		return defaultVal
	}
	if v, ok := f.ctx.ToolConfig[key]; ok {
		return v
	}
	return defaultVal
}

func (f *Facade) SendStatus(message string) error {
	if f.ctx.SendStatus == nil {
		return nil
	}
	return f.ctx.SendStatus(message)
}

func (f *Facade) SendSignal(signal any) error {
	if f.ctx.SendSignal == nil {
		return nil
	}
	return f.ctx.SendSignal(signal)
}

func (f *Facade) SaveArtifact(ctx context.Context, filename string, content []byte, mimeType string) (int, error) {
	svc := f.ctx.ArtifactService
	if svc == nil {
		return 0, ErrNoArtifactService
	}
	loc := svc.Resolve(f.ctx.AgentName, f.ctx.UserID, f.ctx.SessionID, filename)
	version, err := svc.Inner.Save(ctx, loc, content, mimeType)
	if err != nil {
		return 0, err
	}
	f.fireArtifactSaved(ctx, ArtifactSavedInfo{
		Filename:  filename,
		Version:   version,
		MIMEType:  mimeType,
		SizeBytes: int64(len(content)),
	})
	return version, nil
}

func (f *Facade) SaveArtifactWithMetadata(ctx context.Context, filename string, content []byte, mimeType string, opts artifact.MetadataOpts) (*artifact.SaveResult, error) {
	svc := f.ctx.ArtifactService
	if svc == nil {
		return nil, ErrNoArtifactService
	}
	res, err := artifact.SaveWithMetadata(ctx, svc,
		f.ctx.AgentName, f.ctx.UserID, f.ctx.SessionID,
		filename, content, mimeType, opts, slog.Default())
	if err != nil {
		return nil, err
	}
	f.fireArtifactSaved(ctx, ArtifactSavedInfo{
		Filename:    filename,
		Version:     res.DataVersion,
		MIMEType:    mimeType,
		SizeBytes:   int64(len(content)),
		Description: opts.Description,
		Tags:        opts.Tags,
	})
	return res, nil
}

func (f *Facade) fireArtifactSaved(ctx context.Context, info ArtifactSavedInfo) {
	if f.ctx.NotifyArtifactSaved == nil {
		return
	}
	info.ToolCallID = f.ctx.ToolCallID
	defer func() {
		if r := recover(); r != nil {
			slog.Default().With("logger_name", "tool-facade").Warn("NotifyArtifactSaved hook panicked",
				"filename", info.Filename, "version", info.Version, "panic", r)
		}
	}()
	f.ctx.NotifyArtifactSaved(ctx, info)
}

func (f *Facade) SaveArtifactMetadata(ctx context.Context, filename string, version int, meta *artifact.VersionMetadata) error {
	svc := f.ctx.ArtifactService
	if svc == nil {
		return ErrNoArtifactService
	}
	loc := svc.Resolve(f.ctx.AgentName, f.ctx.UserID, f.ctx.SessionID, filename)
	return svc.Inner.SaveMetadata(ctx, loc, version, meta)
}

func (f *Facade) LoadArtifact(ctx context.Context, filename string, version *int) (content []byte, mimeType string, err error) {
	svc := f.ctx.ArtifactService
	if svc == nil {
		return nil, "", ErrNoArtifactService
	}
	loc := svc.Resolve(f.ctx.AgentName, f.ctx.UserID, f.ctx.SessionID, filename)
	return svc.Inner.Load(ctx, loc, version)
}

func (f *Facade) LoadArtifactMetadata(ctx context.Context, filename string, version *int) (*artifact.VersionMetadata, error) {
	svc := f.ctx.ArtifactService
	if svc == nil {
		return nil, ErrNoArtifactService
	}
	loc := svc.Resolve(f.ctx.AgentName, f.ctx.UserID, f.ctx.SessionID, filename)
	return svc.Inner.GetVersionMetadata(ctx, loc, version)
}

func (f *Facade) ListArtifactVersions(ctx context.Context, filename string) ([]int, error) {
	svc := f.ctx.ArtifactService
	if svc == nil {
		return nil, ErrNoArtifactService
	}
	loc := svc.Resolve(f.ctx.AgentName, f.ctx.UserID, f.ctx.SessionID, filename)
	return svc.Inner.ListVersions(ctx, loc)
}

func (f *Facade) SaveArtifactVersion(ctx context.Context, filename string, version int, content []byte, mimeType string) error {
	svc := f.ctx.ArtifactService
	if svc == nil {
		return ErrNoArtifactService
	}
	loc := svc.Resolve(f.ctx.AgentName, f.ctx.UserID, f.ctx.SessionID, filename)
	return svc.Inner.SaveVersion(ctx, loc, version, content, mimeType)
}

func (f *Facade) LoadArtifactFromScope(ctx context.Context, sessionID, filename string, version *int) (content []byte, mimeType string, err error) {
	svc := f.ctx.ArtifactService
	if svc == nil {
		return nil, "", ErrNoArtifactService
	}
	loc := svc.Resolve(f.ctx.AgentName, f.ctx.UserID, sessionID, filename)
	return svc.Inner.Load(ctx, loc, version)
}

func (f *Facade) ListArtifacts(ctx context.Context) ([]string, error) {
	svc := f.ctx.ArtifactService
	if svc == nil {
		return nil, ErrNoArtifactService
	}
	scope := artifact.KeyScope{
		AppName:   f.ctx.AgentName,
		UserID:    f.ctx.UserID,
		SessionID: f.ctx.SessionID,
	}
	if svc.Scope == artifact.ScopeNamespace {
		scope.AppName = svc.Namespace
	}
	return svc.Inner.ListKeys(ctx, scope)
}
