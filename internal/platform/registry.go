// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
)

const (
	ExtensionURIGatewayRole       = a2a.ExtensionURIGatewayRole
	ExtensionURIDeployment        = "https://solace.com/a2a/extensions/sam/deployment"
	ExtensionURIDisplayName       = "https://solace.com/a2a/extensions/display-name"
	ExtensionURIToolRegistrations = "https://solace.com/a2a/extensions/sam/tool-registrations"
)

type AgentRegistry struct {
	cards *discovery.Registry
}

func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{cards: discovery.NewRegistry()}
}

func (r *AgentRegistry) AddOrUpdate(card a2a.AgentCard) bool {
	return r.AddOrUpdateInstance(card, "")
}

func (r *AgentRegistry) AddOrUpdateInstance(card a2a.AgentCard, instanceID string) bool {
	return r.cards.Observe(card, instanceID) == discovery.Added
}

func (r *AgentRegistry) Get(name string) *a2a.AgentCard {
	return r.cards.Get(name)
}

func (r *AgentRegistry) Instances(name string) []discovery.Instance {
	return r.cards.Instances(name)
}

func DisplayName(card *a2a.AgentCard) string {
	if card == nil {
		return ""
	}
	if v := getExtensionParam(card, ExtensionURIDisplayName, "display_name"); v != "" {
		return v
	}
	return card.Name
}

func (r *AgentRegistry) Names() []string {
	return r.cards.Names()
}

func (r *AgentRegistry) List() []a2a.AgentCard {
	return r.cards.List()
}

func (r *AgentRegistry) Count() int {
	return r.cards.Count()
}

func (r *AgentRegistry) Remove(name string) bool {
	return r.cards.Delete(name)
}

func (r *AgentRegistry) RemoveInstance(name, instanceID string) bool {
	return r.cards.Remove(name, instanceID).Gone
}

func (r *AgentRegistry) apply(m discovery.Message) discovery.Event {
	return r.cards.Apply(m)
}

func (r *AgentRegistry) ExpireOlderThan(cutoff time.Time) []string {
	return r.cards.ExpireOlderThan(cutoff)
}

func (r *AgentRegistry) Clear() {
	r.cards.Clear()
}

type GatewayRegistry struct {
	cards *discovery.Registry
}

func NewGatewayRegistry() *GatewayRegistry {
	return &GatewayRegistry{cards: discovery.NewRegistry()}
}

func (r *GatewayRegistry) AddOrUpdate(card a2a.AgentCard) bool {
	return r.AddOrUpdateInstance(card, "")
}

func (r *GatewayRegistry) AddOrUpdateInstance(card a2a.AgentCard, instanceID string) bool {
	return r.cards.Observe(card, instanceID) == discovery.Added
}

func (r *GatewayRegistry) Get(id string) *a2a.AgentCard {
	return r.cards.Get(id)
}

func (r *GatewayRegistry) IDs() []string {
	return r.cards.Names()
}

func (r *GatewayRegistry) Count() int {
	return r.cards.Count()
}

func (r *GatewayRegistry) Remove(id string) bool {
	return r.cards.Delete(id)
}

func (r *GatewayRegistry) RemoveInstance(id, instanceID string) bool {
	return r.cards.Remove(id, instanceID).Gone
}

func (r *GatewayRegistry) apply(m discovery.Message) discovery.Event {
	return r.cards.Apply(m)
}

func (r *GatewayRegistry) ExpireOlderThan(cutoff time.Time) []string {
	return r.cards.ExpireOlderThan(cutoff)
}

func (r *GatewayRegistry) Clear() {
	r.cards.Clear()
}

func (r *GatewayRegistry) GetGatewayType(id string) string {
	card := r.Get(id)
	if card == nil {
		return ""
	}
	return getExtensionParam(card, ExtensionURIGatewayRole, "gateway_type")
}

func (r *GatewayRegistry) GetGatewayNamespace(id string) string {
	card := r.Get(id)
	if card == nil {
		return ""
	}
	return getExtensionParam(card, ExtensionURIGatewayRole, "namespace")
}

func (r *GatewayRegistry) GetDeploymentID(id string) string {
	card := r.Get(id)
	if card == nil {
		return ""
	}
	return getExtensionParam(card, ExtensionURIDeployment, "deployment_id")
}

func IsGatewayCard(card *a2a.AgentCard) bool {
	return a2a.IsGatewayCard(*card)
}

func getExtensionParam(card *a2a.AgentCard, uri, param string) string {
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == uri {
			if v, ok := ext.Params[param].(string); ok {
				return v
			}
		}
	}
	return ""
}

func GetAgentDeploymentID(card *a2a.AgentCard) string {
	return getExtensionParam(card, ExtensionURIDeployment, "deployment_id")
}

type ToolEntryRegistration struct {
	ToolType    string
	ConnectorID string
	Name        string
	Status      string
	Error       string
	ToolCount   int
	ToolNames   []string
}

func AgentToolRegistrations(card *a2a.AgentCard) []ToolEntryRegistration {
	if card == nil {
		return nil
	}
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != ExtensionURIToolRegistrations {
			continue
		}
		raw, _ := ext.Params["entries"].([]any)
		regs := make([]ToolEntryRegistration, 0, len(raw))
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			reg := ToolEntryRegistration{
				ToolType:    stringParam(m, "tool_type"),
				ConnectorID: stringParam(m, "connector_id"),
				Name:        stringParam(m, "name"),
				Status:      stringParam(m, "status"),
				Error:       stringParam(m, "error"),
			}
			if reg.Status == "" {
				continue
			}
			switch n := m["tool_count"].(type) {
			case float64:
				reg.ToolCount = int(n)
			case int:
				reg.ToolCount = n
			}
			reg.ToolNames = stringSliceParam(m, "tool_names")
			regs = append(regs, reg)
		}
		if len(regs) == 0 {
			return nil
		}
		return regs
	}
	return nil
}

type Problem struct {
	ID       string
	Severity string
	Detail   string
	Since    time.Time
}

func (p Problem) MarshalJSON() ([]byte, error) {
	since := ""
	if !p.Since.IsZero() {
		since = p.Since.UTC().Format("2006-01-02T15:04:05.000000+00:00")
	}
	return json.Marshal(struct {
		ID       string `json:"id"`
		Severity string `json:"severity"`
		Detail   string `json:"detail"`
		Since    string `json:"since"`
	}{ID: p.ID, Severity: p.Severity, Detail: p.Detail, Since: since})
}

func (p *Problem) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID       string `json:"id"`
		Severity string `json:"severity"`
		Detail   string `json:"detail"`
		Since    string `json:"since"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	p.ID = wire.ID
	p.Severity = wire.Severity
	p.Detail = wire.Detail
	if wire.Since == "" {
		p.Since = time.Time{}
		return nil
	}
	since, err := time.Parse("2006-01-02T15:04:05.000000+00:00", wire.Since)
	if err != nil {
		return fmt.Errorf("parse problem since %q: %w", wire.Since, err)
	}
	p.Since = since
	return nil
}

func (r *GatewayRegistry) RuntimeProblems(id string) []Problem {
	return unionProblems(r.cards.Instances(id))
}

func (r *AgentRegistry) RuntimeProblems(name string) []Problem {
	return unionProblems(r.cards.Instances(name))
}

func unionProblems(instances []discovery.Instance) []Problem {
	var out []Problem
	seen := map[string]int{}
	for _, inst := range instances {
		for _, p := range RuntimeProblemsFromCard(&inst.Card) {
			if i, ok := seen[p.ID]; ok {
				if p.Severity == health.Fatal.String() {
					out[i].Severity = p.Severity
				}
				continue
			}
			seen[p.ID] = len(out)
			out = append(out, p)
		}
	}
	return out
}

func (r *GatewayRegistry) Serving(id string) bool {
	for _, inst := range r.cards.Instances(id) {
		if !slices.ContainsFunc(RuntimeProblemsFromCard(&inst.Card), func(p Problem) bool { return p.ID == health.StartFailedProblemID }) {
			return true
		}
	}
	return false
}

func RuntimeProblemsFromCard(card *a2a.AgentCard) []Problem {
	if card == nil {
		return nil
	}
	raw := a2a.HealthProblemsFromCard(*card)
	if len(raw) == 0 {
		return nil
	}
	problems := make([]Problem, 0, len(raw))
	for _, p := range raw {
		problems = append(problems, Problem{ID: p.ID, Severity: p.Severity.String(), Detail: p.Detail, Since: p.Since})
	}
	return problems
}

const (
	RuntimeStatusRunning      = "running"
	RuntimeStatusDegraded     = "degraded"
	RuntimeStatusStarting     = "starting"
	RuntimeStatusDisconnected = "disconnected"
	RuntimeStatusStopped      = "stopped"
)

type RuntimeStatus struct {
	status   string
	problems []Problem
}

func (r RuntimeStatus) Status() string {
	if r.status == "" {
		return RuntimeStatusStopped
	}
	return r.status
}

func (r RuntimeStatus) Problems() []Problem { return r.problems }

func (r RuntimeStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Status   string    `json:"status"`
		Problems []Problem `json:"problems,omitempty"`
	}{Status: r.Status(), Problems: r.problems})
}

func (r *RuntimeStatus) UnmarshalJSON(data []byte) error {
	var wire struct {
		Status   string    `json:"status"`
		Problems []Problem `json:"problems,omitempty"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	status := wire.Status
	if len(wire.Problems) > 0 {
		status = RuntimeStatusDegraded
	}
	r.status = status
	r.problems = wire.Problems
	return nil
}

func RunningStatus(problems []Problem) RuntimeStatus {
	status := RuntimeStatusRunning
	if len(problems) > 0 {
		status = RuntimeStatusDegraded
	}
	return RuntimeStatus{status: status, problems: problems}
}

func StartingStatus() RuntimeStatus { return RuntimeStatus{status: RuntimeStatusStarting} }

func DisconnectedStatus() RuntimeStatus { return RuntimeStatus{status: RuntimeStatusDisconnected} }

func StoppedStatus() RuntimeStatus { return RuntimeStatus{status: RuntimeStatusStopped} }

func stringParam(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func stringSliceParam(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
