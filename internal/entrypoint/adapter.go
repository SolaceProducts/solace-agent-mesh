// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

type MountKind int

const (
	MountNone MountKind = iota
	MountAtPrefix
	MountNamed
)

type MountSpec struct {
	Kind   MountKind
	Prefix string
}

type GatewayAdapter interface {
	Init(ctx context.Context, core *GatewayCore) error

	Start(ctx context.Context) error

	Stop(ctx context.Context) error

	MountIntent() MountSpec

	OnStatusUpdate(ctx context.Context, taskID string, event *a2a.TaskStatusUpdateEvent, raw []byte)

	OnArtifactUpdate(ctx context.Context, taskID string, event *a2a.TaskArtifactUpdateEvent, raw []byte)

	OnFinalResponse(ctx context.Context, taskID string, task *a2a.Task, raw []byte)

	OnError(ctx context.Context, taskID string, err *a2a.Error, raw []byte)

	OnAgentDiscovered(card *a2a.AgentCard)

	OnAgentRemoved(agentName string)
}

type CapabilityProvider interface {
	Capabilities() map[string]bool
}

type ActivityEventEmitter interface {
	OnActivityEvent(ctx context.Context, taskID, topic string, payload map[string]any)
}

type ActivityWatchers interface {
	WatchingTask(taskID string) bool
}

type ForwardedActivityView interface {
	ForwardedActivity() ActivityEventEmitter
}

type StatusTextRenderer interface {
	RendersStatusText(surface string) bool
}

type SchedulerHost interface {
	HostsScheduler() bool
}

type TaskEndedElsewhereHandler interface {
	OnTaskEndedElsewhere(taskID string)
}

type TaskRecordKeptUntilAck interface {
	KeepsTaskRecordUntilAck()
}

type LeaderIngressAdapter interface {
	RunIngress(ctx context.Context, term uint64)
}

type DeferredHTTPAdapter interface {
	SetOnHandlerReady(cb func(http.Handler))
}

type MachineIngressAdapter interface {
	MachineIngress() bool

	EntrypointType() string
}

type RelayedSurfaceAdapter interface {
	RelayedSurface() string
}

type AdapterQueueDeprovisioner interface {
	DeprovisionAdapterQueues(ctx context.Context) error
}

type Reconfigurer interface {
	Reconfigure(ctx context.Context, adapterConfig map[string]any) error
}

var ErrRestartRequired = errors.New("the entrypoint must restart to take this configuration")

func AdapterConfig(appConfig map[string]any) map[string]any {
	adapter, _ := appConfig["gateway_adapter"].(map[string]any)
	adapterConfig, _ := adapter["adapter_config"].(map[string]any)
	return adapterConfig
}

type LoggingAdapter struct {
	logger *slog.Logger
}

func NewLoggingAdapter(logger *slog.Logger) *LoggingAdapter {
	return &LoggingAdapter{logger: logger}
}

func (a *LoggingAdapter) log() *slog.Logger {
	if a.logger == nil {
		return slog.Default()
	}
	return a.logger
}

func (a *LoggingAdapter) Init(_ context.Context, _ *GatewayCore) error { return nil }

func (a *LoggingAdapter) Start(_ context.Context) error { return nil }

func (a *LoggingAdapter) Stop(_ context.Context) error { return nil }

func (a *LoggingAdapter) MountIntent() MountSpec { return MountSpec{Kind: MountNone} }

func (a *LoggingAdapter) OnStatusUpdate(_ context.Context, taskID string, event *a2a.TaskStatusUpdateEvent, _ []byte) {
	a.log().Debug("status update", "taskID", taskID, "state", event.Status.State, "final", event.Final)
}

func (a *LoggingAdapter) OnArtifactUpdate(_ context.Context, taskID string, event *a2a.TaskArtifactUpdateEvent, _ []byte) {
	a.log().Debug("artifact update", "taskID", taskID, "artifactID", event.Artifact.ArtifactID)
}

func (a *LoggingAdapter) OnFinalResponse(_ context.Context, taskID string, task *a2a.Task, _ []byte) {
	a.log().Debug("final response", "taskID", taskID, "state", task.Status.State)
}

func (a *LoggingAdapter) OnError(_ context.Context, taskID string, err *a2a.Error, _ []byte) {
	a.log().Warn("error response", "taskID", taskID, "code", err.Code, "message", err.Message)
}

func (a *LoggingAdapter) OnAgentDiscovered(card *a2a.AgentCard) {
	a.log().Info("agent discovered", "agentName", card.Name, "skills", len(card.Skills))
}

func (a *LoggingAdapter) OnAgentRemoved(agentName string) {
	a.log().Info("agent removed", "agentName", agentName)
}

var _ GatewayAdapter = (*LoggingAdapter)(nil)
