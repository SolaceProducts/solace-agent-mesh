// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
)

var _ control.Manageable = (*Worker)(nil)

type modelBindingWriter interface {
	UpsertModel(ctx context.Context, alias string, rawConfig map[string]any) error
	DeleteModel(alias string) bool
	Refresh(alias string)
	Snapshot() map[string]any
}

type modelChangeFollower interface {
	OnChange(fn func(key string))
	Want(fn func(key string) bool)
}

func (w *Worker) ManagementEndpoints() []control.Endpoint {
	return []control.Endpoint{
		{Method: "get", Path: "bindings", Description: "Inspect the STR's current pod-level model bindings"},
		{Method: "patch", Path: "bindings", Description: "Upsert pod-level model bindings (sparse)"},
	}
}

func (w *Worker) HandleManagement(ctx context.Context, method string, path []string, body json.RawMessage, _ *auth.Identity) (any, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("missing resource path")
	}
	switch path[0] {
	case "bindings":
		return w.handleBindingsResource(ctx, method, path[1:], body)
	default:
		return nil, fmt.Errorf("unknown resource: %s", path[0])
	}
}

func (w *Worker) bindingsWriter() (modelBindingWriter, error) {
	b := w.svc.Bindings()
	if b == nil {
		return nil, fmt.Errorf("model bindings not configured on this STR worker")
	}
	mw, ok := b.(modelBindingWriter)
	if !ok {
		return nil, fmt.Errorf("model bindings registry does not support push updates")
	}
	return mw, nil
}

func (w *Worker) handleBindingsResource(ctx context.Context, method string, sub []string, body json.RawMessage) (any, error) {
	if len(sub) != 0 {
		return nil, fmt.Errorf("unsupported bindings sub-path: %v", sub)
	}
	writer, err := w.bindingsWriter()
	if err != nil {
		return nil, err
	}
	switch method {
	case "get":
		return writer.Snapshot(), nil
	case "patch":
		return w.handlePatchBindings(ctx, writer, body)
	default:
		return nil, fmt.Errorf("method not allowed on bindings: %s", method)
	}
}

type bindingsPatch struct {
	Models map[string]map[string]any `json:"models"`
}

func aliasesFromSnapshot(snap map[string]any) map[string]bool {
	models, ok := snap["models"].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]bool, len(models))
	for alias := range models {
		out[alias] = true
	}
	return out
}

func (w *Worker) handlePatchBindings(ctx context.Context, writer modelBindingWriter, body json.RawMessage) (any, error) {
	if len(body) == 0 {
		return nil, errors.New("request body required")
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var patch bindingsPatch
	if err := dec.Decode(&patch); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	if len(patch.Models) == 0 {
		return nil, fmt.Errorf("request body must contain at least one model binding")
	}
	for alias := range patch.Models {
		if alias == "" {
			return nil, fmt.Errorf("model alias must be non-empty")
		}
	}

	wantAlias, _ := w.toolModelAlias()
	alreadyInstalled := aliasesFromSnapshot(writer.Snapshot())

	applied, skipped := 0, 0
	for alias, cfg := range patch.Models {
		if alias != wantAlias && !alreadyInstalled[alias] {
			skipped++
			continue
		}
		if cfg == nil {
			writer.DeleteModel(alias)
			w.tempRejects.Forget(alias)
			writer.Refresh(alias)
			applied++
			continue
		}
		if err := writer.UpsertModel(ctx, alias, cfg); err != nil {
			return nil, fmt.Errorf("apply model %q: %w", alias, err)
		}
		w.tempRejects.Forget(alias)
		writer.Refresh(alias)
		applied++
	}
	w.logger.Debug("applied model bindings push",
		"applied", applied, "skipped", skipped, "wantAlias", wantAlias)
	return writer.Snapshot(), nil
}
