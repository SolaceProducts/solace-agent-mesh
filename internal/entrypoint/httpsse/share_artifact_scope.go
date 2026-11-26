// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"errors"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type shareArtifactScope struct {
	requester string
	link      *store.SharedLink
	artifacts *artifact.ScopedService
	logger    *slog.Logger
	load      func() (entrypoint.ReferencedUserFiles, error)

	loaded     bool
	referenced entrypoint.ReferencedUserFiles
	decided    map[string]bool
	failure    error
}

func (h *shareLinkHandler) scopeFromLoadedTasks(requester string, link *store.SharedLink, tasks []*store.ChatTask) *shareArtifactScope {
	return &shareArtifactScope{
		requester: requester,
		link:      link,
		artifacts: h.artSvc,
		logger:    h.logger,
		load: func() (entrypoint.ReferencedUserFiles, error) {
			return entrypoint.UserScopedFilesReferencedBy(tasks, link.UserID)
		},
	}
}

func (h *shareLinkHandler) scopeFromStoredTasks(ctx context.Context, requester string, link *store.SharedLink) *shareArtifactScope {
	return &shareArtifactScope{
		requester: requester,
		link:      link,
		artifacts: h.artSvc,
		logger:    h.logger,
		load: func() (entrypoint.ReferencedUserFiles, error) {
			tasks, err := h.chatRepo.ListBySession(ctx, link.SessionID)
			if err != nil {
				return nil, err
			}
			return entrypoint.UserScopedFilesReferencedBy(tasks, link.UserID)
		},
	}
}

func (s *shareArtifactScope) refuses(ctx context.Context, filename string) bool {
	return refusesUnreferencedUserScopedName(s.requester, s.link.UserID, filename, func(name string) bool {
		return s.vouches(ctx, name)
	})
}

func (s *shareArtifactScope) pinnedVersion(filename string) *int {
	if !s.decided[filename] {
		return nil
	}
	version := s.referenced[filename]
	return &version
}

func (s *shareArtifactScope) unavailable() error {
	return s.failure
}

func (s *shareArtifactScope) vouches(ctx context.Context, filename string) bool {
	if decision, decided := s.decided[filename]; decided {
		return decision
	}
	if s.failure != nil {
		return false
	}
	decision := s.decide(ctx, filename)
	if s.failure != nil {
		return false
	}
	if s.decided == nil {
		s.decided = map[string]bool{}
	}
	s.decided[filename] = decision
	return decision
}

func (s *shareArtifactScope) decide(ctx context.Context, filename string) bool {
	referenced := s.referencedUserFiles()
	if s.failure != nil {
		return false
	}
	if _, named := referenced[filename]; !named {
		return false
	}
	latest, err := s.latestVersion(ctx, filename)
	if err != nil {
		s.failure = err
		return false
	}
	return referenced.Has(filename, latest)
}

func (s *shareArtifactScope) referencedUserFiles() entrypoint.ReferencedUserFiles {
	if s.loaded {
		return s.referenced
	}
	s.loaded = true
	referenced, err := s.load()
	switch {
	case errors.Is(err, entrypoint.ErrUnreadableTasks):
		s.logger.Warn("share link: chat tasks left out of the files the shared session references",
			"shareID", s.link.ShareID, "userID", s.requester, "error", err)
	case err != nil:
		s.failure = err
		referenced = nil
	}
	s.referenced = referenced
	return referenced
}

func (s *shareArtifactScope) latestVersion(ctx context.Context, filename string) (int, error) {
	if s.artifacts == nil {
		return -1, nil
	}
	return entrypoint.LatestArtifactVersion(ctx, s.artifacts, s.link.UserID, s.link.SessionID, filename)
}
