// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embed"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

const serveArtifactSizeCap = 10_000_000

func newSkillEmbedResolveCheck(logger *slog.Logger) str.EmbedResolveCheckFunc {
	return func(ctx context.Context, candidate string, arts *artifact.ScopedService, agentName, userID, sessionID string) (string, error) {
		rctx := &embed.ResolveContext{
			Artifacts: arts,
			AgentName: agentName,
			UserID:    userID,
			SessionID: sessionID,
			Config: embed.ResolveConfig{
				MaxDepth:             12,
				MaxArtifactSizeBytes: serveArtifactSizeCap,
				MaxTotalSizeBytes:    embed.DefaultMaxTotalSizeBytes,
			},
			Logger: logger,
		}
		resolved, _, err := embed.ResolveEmbedsInString(ctx, candidate, embed.ModeA2AMessageToUser, rctx)
		return resolved, err
	}
}

func NewSTRWorker(svc runtime.Services, appCfg config.Config) (*str.Worker, error) {
	workerCfg, err := str.ParseWorkerConfig(appCfg)
	if err != nil {
		return nil, fmt.Errorf("parse worker config: %w", err)
	}
	return NewSTRWorkerFromConfig(svc, workerCfg, appCfg)
}

func NewSTRWorkerFromConfig(svc runtime.Services, workerCfg *str.WorkerConfig, appCfg config.Config) (*str.Worker, error) {
	if volSvc := svc.Volumes(); volSvc != nil {
		workerCfg.VolumeService = volSvc
	} else if volumeRoot := appCfg.GetString(config.KeyVolumeRoot); volumeRoot != "" {
		volSvc, volErr := volume.NewDirectoryStore(volumeRoot, slog.Default())
		if volErr != nil {
			return nil, fmt.Errorf("create volume service: %w", volErr)
		}
		workerCfg.VolumeService = volSvc
	}

	w := str.NewWorker(svc, *workerCfg)
	w.SetEmbedResolveCheck(newSkillEmbedResolveCheck(svc.Logger().With("logger_name", "skill-embed-resolve")))
	return w, nil
}

func BuildSTRRuntimeOptions(ctx context.Context, rootCfg map[string]any, appCfg config.Config, logger *slog.Logger) []runtime.Option {
	opts := []runtime.Option{
		runtime.WithLogger(logger),
	}

	appCfgMap := config.AsMap(appCfg)
	var metrics runtime.Metrics
	if rootCfg != nil || appCfgMap != nil {
		metrics = createMetricsRecorder(rootCfg, []map[string]any{appCfgMap}, logger, "sam-str")
	}
	if metrics != nil {
		opts = append(opts, runtime.WithMetrics(metrics))
	}

	if artRaw := appCfg.GetRaw("artifact_service"); artRaw != nil {
		if artCfg, ok := artRaw.(map[string]any); ok {
			namespace := appCfg.GetString(config.KeyNamespace)
			artAppCfgMap := map[string]any{config.KeyNamespace: namespace}
			svc := samapp.CreateArtifactService(artCfg, artAppCfgMap)
			if svc != nil {
				opts = append(opts, runtime.WithArtifacts(svc))
			}
		}
	}

	if modelRaw := appCfg.GetRaw("model"); modelRaw != nil {
		llmOpts, err := buildLLMOptions(ctx, modelRaw, logger)
		if err != nil {
			logger.Warn("STR LLM client construction failed; Go-tool LLM callbacks will be unavailable",
				"error", err)
		} else {
			opts = append(opts, llmOpts...)
		}
	}

	return opts
}
