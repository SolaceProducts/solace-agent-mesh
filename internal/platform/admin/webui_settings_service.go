// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/webuisettings"
)

type WebuiSettingsService struct {
	repo    *store.PlatformWebuiSettingsRepo
	changes authzChangeOutbox
	logger  *slog.Logger
}

func NewWebuiSettingsService(repo *store.PlatformWebuiSettingsRepo, logger *slog.Logger) *WebuiSettingsService {
	if logger == nil {
		logger = slog.Default()
	}
	return &WebuiSettingsService{
		repo:    repo,
		changes: authzChangeOutbox{db: repo},
		logger:  logger.With("logger_name", "webui-settings"),
	}
}

func (s *WebuiSettingsService) WithOutbox(repo *store.OutboxRepo, notifier platpkg.OutboxNotifier) {
	s.changes.repo = repo
	s.changes.notifier = notifier
}

func (s *WebuiSettingsService) Get(ctx context.Context) (*WebuiSettingsResponse, error) {
	row, err := s.repo.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get webui settings: %w", err)
	}
	if row == nil {
		return &WebuiSettingsResponse{}, nil
	}
	return toWebuiSettingsResponse(row), nil
}

func (s *WebuiSettingsService) GetRow(ctx context.Context) (*store.PlatformWebuiSettings, error) {
	return s.repo.Get(ctx)
}

func (s *WebuiSettingsService) Set(ctx context.Context, req UpdateWebuiSettingsRequest, actor string) (*WebuiSettingsResponse, error) {
	if err := validateWebuiSettings(req); err != nil {
		return nil, err
	}

	allNil := req.AppName == nil && req.WelcomeMessage == nil && req.CollectFeedback == nil &&
		req.PublishFeedback == nil && req.FeedbackTaskInfoLevel == nil &&
		req.DisclaimerText == nil && req.LogoURL == nil &&
		req.SmallLogoURL == nil && req.SystemPurpose == nil && req.ResponseFormat == nil

	if allNil {
		if err := s.changes.commit(ctx, webuiSettingsOutboxChanged, func(tx *dbutil.Tx) (string, error) {
			return webuiSettingsOutboxAggregateID, s.repo.DeleteInTx(ctx, tx)
		}); err != nil {
			return nil, fmt.Errorf("clear webui settings: %w", err)
		}
		s.audit(actor, "clear", nil, nil)
	} else {
		row := &store.PlatformWebuiSettings{
			AppName:               req.AppName,
			WelcomeMessage:        req.WelcomeMessage,
			CollectFeedback:       req.CollectFeedback,
			PublishFeedback:       req.PublishFeedback,
			FeedbackTaskInfoLevel: req.FeedbackTaskInfoLevel,
			DisclaimerText:        req.DisclaimerText,
			LogoURL:               req.LogoURL,
			SmallLogoURL:          req.SmallLogoURL,
			SystemPurpose:         req.SystemPurpose,
			ResponseFormat:        req.ResponseFormat,
		}
		if err := s.changes.commit(ctx, webuiSettingsOutboxChanged, func(tx *dbutil.Tx) (string, error) {
			return webuiSettingsOutboxAggregateID, s.repo.SetInTx(ctx, tx, row, actor)
		}); err != nil {
			return nil, fmt.Errorf("persist webui settings: %w", err)
		}
		s.audit(actor, "set", req.PublishFeedback, req.FeedbackTaskInfoLevel)
	}

	resp, err := s.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("webui settings write committed but read-back failed: %w", err)
	}
	return resp, nil
}

func (s *WebuiSettingsService) audit(actor, action string, publishFeedback *bool, feedbackTaskInfoLevel *string) {
	args := []any{
		"event", "platform_webui_settings_mutation",
		"action", action,
		"actor", actor,
	}
	if publishFeedback != nil {
		args = append(args, "publishFeedback", *publishFeedback)
	}
	if feedbackTaskInfoLevel != nil {
		args = append(args, "feedbackTaskInfoLevel", *feedbackTaskInfoLevel)
	}
	s.logger.Info("webui settings audit", args...)
}

const (
	maxWebuiAppNameLen        = 64
	maxWebuiWelcomeMessageLen = 2000
	maxWebuiDisclaimerTextLen = 800
	maxWebuiPromptFieldLen    = platpkg.MaxPromptFieldLen
	maxWebuiLogoURLLen        = 2048
	maxWebuiLogoDataURILen    = 262144
)

const errTooLongFmt = "must be at most %d characters"

func validateWebuiSettings(req UpdateWebuiSettingsRequest) error {
	details := map[string][]string{}
	addErr := func(field, msg string) { details[field] = append(details[field], msg) }
	checkLen := func(field string, val *string, max int) {
		if val != nil && len([]rune(*val)) > max {
			addErr(field, fmt.Sprintf(errTooLongFmt, max))
		}
	}

	checkLen("appName", req.AppName, maxWebuiAppNameLen)
	if req.AppName != nil && strings.ContainsFunc(*req.AppName, unicode.IsControl) {
		addErr("appName", "must not contain control characters or newlines")
	}
	checkLen("welcomeMessage", req.WelcomeMessage, maxWebuiWelcomeMessageLen)
	checkLen("disclaimerText", req.DisclaimerText, maxWebuiDisclaimerTextLen)
	checkLen("systemPurpose", req.SystemPurpose, maxWebuiPromptFieldLen)
	checkLen("responseFormat", req.ResponseFormat, maxWebuiPromptFieldLen)
	if req.LogoURL != nil {
		for _, msg := range validateLogoURL(*req.LogoURL) {
			addErr("logoUrl", msg)
		}
	}
	if req.SmallLogoURL != nil {
		for _, msg := range validateLogoURL(*req.SmallLogoURL) {
			addErr("smallLogoUrl", msg)
		}
	}
	if req.FeedbackTaskInfoLevel != nil && !webuisettings.IsValidFeedbackTaskInfoLevel(*req.FeedbackTaskInfoLevel) {
		addErr("feedbackTaskInfoLevel", fmt.Sprintf("must be one of %s", strings.Join(webuisettings.ValidFeedbackTaskInfoLevels(), ", ")))
	}

	if len(details) > 0 {
		return &ValidationError{Message: "validation failed", Details: details}
	}
	return nil
}

func validateLogoURL(v string) []string {
	if v == "" {
		return nil
	}
	lower := strings.ToLower(v)
	switch {
	case strings.HasPrefix(lower, "data:image/"):
		if len(v) > maxWebuiLogoDataURILen {
			return []string{fmt.Sprintf("data: URI must be at most %d bytes", maxWebuiLogoDataURILen)}
		}
		return nil
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
	case strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//"):
	default:
		return []string{"must be an http(s) URL, a site-relative path, or a data:image/* URI"}
	}
	if len(v) > maxWebuiLogoURLLen {
		return []string{fmt.Sprintf(errTooLongFmt, maxWebuiLogoURLLen)}
	}
	return nil
}

func WebuiSettingsFromRow(row *store.PlatformWebuiSettings) *webuisettings.Settings {
	if row == nil {
		return nil
	}
	return &webuisettings.Settings{
		AppName:               row.AppName,
		WelcomeMessage:        row.WelcomeMessage,
		CollectFeedback:       row.CollectFeedback,
		PublishFeedback:       row.PublishFeedback,
		FeedbackTaskInfoLevel: row.FeedbackTaskInfoLevel,
		DisclaimerText:        row.DisclaimerText,
		LogoURL:               row.LogoURL,
		SmallLogoURL:          row.SmallLogoURL,
		SystemPurpose:         row.SystemPurpose,
		ResponseFormat:        row.ResponseFormat,
	}
}

func toWebuiSettingsResponse(row *store.PlatformWebuiSettings) *WebuiSettingsResponse {
	return &WebuiSettingsResponse{
		AppName:               row.AppName,
		WelcomeMessage:        row.WelcomeMessage,
		CollectFeedback:       row.CollectFeedback,
		PublishFeedback:       row.PublishFeedback,
		FeedbackTaskInfoLevel: row.FeedbackTaskInfoLevel,
		DisclaimerText:        row.DisclaimerText,
		LogoURL:               row.LogoURL,
		SmallLogoURL:          row.SmallLogoURL,
		SystemPurpose:         row.SystemPurpose,
		ResponseFormat:        row.ResponseFormat,
		CreatedBy:             row.CreatedBy,
		UpdatedBy:             row.UpdatedBy,
		CreatedAt:             Timestamp(row.CreatedTime),
		UpdatedAt:             Timestamp(row.UpdatedTime),
	}
}
