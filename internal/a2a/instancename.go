// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"strings"

	"github.com/google/uuid"
)

type ComponentKind string

const (
	KindAgent    ComponentKind = "agent"
	KindWorkflow ComponentKind = "workflow"
)

func InstanceName(kind ComponentKind, id string) string {
	return string(kind) + "_" + strings.ReplaceAll(id, "-", "_")
}

const WorkflowToolPrefix = "workflow_"

func WorkflowToolName(cardName string) string {
	return WorkflowToolPrefix + strings.ReplaceAll(cardName, "-", "_")
}

func IDFromInstanceName(name string) (id string, kind ComponentKind, ok bool) {
	var body string
	switch {
	case strings.HasPrefix(name, string(KindAgent)+"_"):
		kind, body = KindAgent, strings.TrimPrefix(name, string(KindAgent)+"_")
	case strings.HasPrefix(name, string(KindWorkflow)+"_"):
		kind, body = KindWorkflow, strings.TrimPrefix(name, string(KindWorkflow)+"_")
	default:
		return "", "", false
	}
	dashed := strings.ReplaceAll(body, "_", "-")
	if _, err := uuid.Parse(dashed); err != nil {
		return "", "", false
	}
	return dashed, kind, true
}

func CanonicalComponentID(instanceName string) string {
	if id, _, ok := IDFromInstanceName(instanceName); ok {
		return id
	}
	return instanceName
}
