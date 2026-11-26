// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrVersionConflict = errors.New("artifact version conflict")

type Service interface {
	Save(ctx context.Context, loc Locator, content []byte, mimeType string) (int, error)

	SaveVersion(ctx context.Context, loc Locator, version int, content []byte, mimeType string) error

	Load(ctx context.Context, loc Locator, version *int) (content []byte, mimeType string, err error)

	Delete(ctx context.Context, loc Locator) error

	ListKeys(ctx context.Context, scope KeyScope) ([]string, error)

	ListVersions(ctx context.Context, loc Locator) ([]int, error)

	GetVersionMetadata(ctx context.Context, loc Locator, version *int) (*VersionMetadata, error)

	SaveMetadata(ctx context.Context, loc Locator, version int, meta *VersionMetadata) error

	ReaderOpener
}

type Unwrapper interface {
	Inner() Service
}

type Locator struct {
	AppName   string
	UserID    string
	SessionID string
	Filename  string
}

type KeyScope struct {
	AppName   string
	UserID    string
	SessionID string
}

type VersionMetadata struct {
	MIMEType    string         `json:"mime_type"`
	Description string         `json:"description,omitempty"`
	SizeBytes   int64          `json:"size_bytes"`
	Schema      map[string]any `json:"schema,omitempty"`
	Custom      map[string]any `json:"custom,omitempty"`
	ModifiedAt  time.Time      `json:"modified_at,omitempty"`
}

func CopyLatest(ctx context.Context, svc Service, from, to Locator) error {
	content, mimeType, err := svc.Load(ctx, from, nil)
	if err != nil {
		return fmt.Errorf("load source artifact %q: %w", from.Filename, err)
	}
	if content == nil {
		return fmt.Errorf("source artifact %q not found", from.Filename)
	}

	version, err := svc.Save(ctx, to, content, mimeType)
	if err != nil {
		return fmt.Errorf("save destination artifact %q: %w", to.Filename, err)
	}

	meta, err := svc.GetVersionMetadata(ctx, from, nil)
	if err != nil {
		return fmt.Errorf("read source metadata for %q: %w", from.Filename, err)
	}
	if meta != nil {
		if err := svc.SaveMetadata(ctx, to, version, meta); err != nil {
			return fmt.Errorf("save destination metadata for %q: %w", to.Filename, err)
		}
	}

	return nil
}

func NormalizeNamespace(namespace string) string {
	trimmed := strings.TrimRight(namespace, "/")
	if trimmed == "" {
		return namespace
	}
	return trimmed
}

const ScopeNamespace = "namespace"

type ScopedService struct {
	Inner     Service
	Scope     string
	Namespace string
}

func (s *ScopedService) Resolve(agentName, userID, sessionID, filename string) Locator {
	appName := agentName
	if s.Scope == ScopeNamespace {
		appName = s.Namespace
	}
	return Locator{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Filename:  filename,
	}
}
