// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package usagefacts

import (
	"cmp"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const MaxSetElements = 32

const MaxArrivingSetElements = 64

const MaxModelElements = 16

type Model struct {
	Family     string
	Provider   string
	ServedFrom string
}

type Facts struct {
	mu                  sync.Mutex
	logger              *slog.Logger
	toolCategories      map[string]struct{}
	connectorCategories map[string]struct{}
	builtinCategories   map[string]struct{}
	toolModelFamilies   map[string]struct{}
	models              map[Model]struct{}
	peerDelegated       bool
	model               Model
}

func (f *Facts) AddToolCategory(category string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolCategories = addBounded(f.toolCategories, category, taxonomy.ToolCategories)
}

func (f *Facts) AddConnectorCategory(category string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connectorCategories = addBounded(f.connectorCategories, category, taxonomy.ConnectorCategories)
}

func (f *Facts) AddBuiltinCategory(category string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.builtinCategories = addBounded(f.builtinCategories, category, taxonomy.BuiltinToolCategories)
}

func (f *Facts) MarkPeerDelegated() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.peerDelegated = true
}

func (f *Facts) RecordModel(m Model) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addModel(m)
	if preferModel(m, f.model) {
		f.model = m
	}
}

func (f *Facts) AddToolModelFamily(family string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolModelFamilies = addBounded(f.toolModelFamilies, family, modelfamily.Families)
}

func (f *Facts) Merge(source map[string]any) {
	if len(source) == 0 {
		return
	}
	delegated, _ := source[a2a.MetadataPeerDelegated].(bool)
	candidate := Model{
		Family:     f.known(a2a.MetadataModelFamily, source[a2a.MetadataModelFamily], modelfamily.Families),
		Provider:   f.known(a2a.MetadataModelProvider, source[a2a.MetadataModelProvider], taxonomy.ProviderLabels),
		ServedFrom: f.known(a2a.MetadataModelType, source[a2a.MetadataModelType], taxonomy.ModelTypes),
	}
	arriving := ReadModels(source[a2a.MetadataModelUsages])

	f.mu.Lock()
	defer f.mu.Unlock()
	f.addModel(candidate)
	for _, model := range arriving {
		f.addModel(model)
	}
	for _, category := range f.knownList(a2a.MetadataToolCategories, source[a2a.MetadataToolCategories], taxonomy.ToolCategories) {
		f.toolCategories = addBounded(f.toolCategories, category, taxonomy.ToolCategories)
	}
	for _, category := range f.knownList(a2a.MetadataConnectorCategories, source[a2a.MetadataConnectorCategories], taxonomy.ConnectorCategories) {
		f.connectorCategories = addBounded(f.connectorCategories, category, taxonomy.ConnectorCategories)
	}
	for _, category := range f.knownList(a2a.MetadataBuiltinCategories, source[a2a.MetadataBuiltinCategories], taxonomy.BuiltinToolCategories) {
		f.builtinCategories = addBounded(f.builtinCategories, category, taxonomy.BuiltinToolCategories)
	}
	for _, toolFamily := range f.knownList(a2a.MetadataToolModelFamilies, source[a2a.MetadataToolModelFamilies], modelfamily.Families) {
		f.toolModelFamilies = addBounded(f.toolModelFamilies, toolFamily, modelfamily.Families)
	}
	if delegated {
		f.peerDelegated = true
	}
	if preferModel(candidate, f.model) {
		f.model = candidate
	}
}

func (f *Facts) SetLogger(logger *slog.Logger) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logger = logger
}

func (f *Facts) logRejected(key, value string) {
	f.mu.Lock()
	logger := f.logger
	f.mu.Unlock()
	logRejectedTo(logger, key, value)
}

func (f *Facts) logRejectedLocked(key, value string) {
	logRejectedTo(f.logger, key, value)
}

func logRejectedTo(logger *slog.Logger, key, value string) {
	if logger != nil {
		logger.Debug("usage facts rejected unapproved value", "key", key, "value", value)
	}
}

func SettleModel(current, candidate Model) Model {
	if preferModel(candidate, current) {
		return candidate
	}
	return current
}

func preferModel(candidate, current Model) bool {
	if candidate == (Model{}) {
		return false
	}
	if current == (Model{}) {
		return true
	}
	return CompareModels(candidate, current) < 0
}

func CompareModels(a, b Model) int {
	if (a.Family == "") != (b.Family == "") {
		if a.Family == "" {
			return 1
		}
		return -1
	}
	return cmp.Or(
		strings.Compare(a.Family, b.Family),
		strings.Compare(a.Provider, b.Provider),
		strings.Compare(a.ServedFrom, b.ServedFrom),
	)
}

func (f *Facts) AddTo(dst map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addTo(dst, true)
}

func (f *Facts) addTo(dst map[string]any, excludeSettledFamily bool) {
	if categories := sorted(f.toolCategories); len(categories) > 0 {
		dst[a2a.MetadataToolCategories] = categories
	}
	if categories := sorted(f.connectorCategories); len(categories) > 0 {
		dst[a2a.MetadataConnectorCategories] = categories
	}
	if categories := sorted(f.builtinCategories); len(categories) > 0 {
		dst[a2a.MetadataBuiltinCategories] = categories
	}
	if f.peerDelegated {
		dst[a2a.MetadataPeerDelegated] = true
	}
	if f.model.Family != "" {
		dst[a2a.MetadataModelFamily] = f.model.Family
	}
	if f.model.Provider != "" {
		dst[a2a.MetadataModelProvider] = f.model.Provider
	}
	if f.model.ServedFrom != "" {
		dst[a2a.MetadataModelType] = f.model.ServedFrom
	}
	if usages := f.sortedModels(); len(usages) > 0 {
		dst[a2a.MetadataModelUsages] = usages
	}
	toolModelFamilies := f.toolModelFamilies
	if excludeSettledFamily {
		toolModelFamilies = exceptSet(toolModelFamilies, f.model.Family)
	}
	if families := sorted(toolModelFamilies); len(families) > 0 {
		dst[a2a.MetadataToolModelFamilies] = families
	}
}

func (f *Facts) sortedModels() []map[string]any {
	models := make([]Model, 0, len(f.models))
	for model := range f.models {
		models = append(models, model)
	}
	slices.SortFunc(models, CompareModels)
	entries := make([]map[string]any, 0, len(models))
	for _, model := range models {
		entry := map[string]any{}
		if model.Family != "" {
			entry[a2a.ModelUsageFamily] = model.Family
		}
		if model.Provider != "" {
			entry[a2a.ModelUsageProvider] = model.Provider
		}
		if model.ServedFrom != "" {
			entry[a2a.ModelUsageServedFrom] = model.ServedFrom
		}
		entries = append(entries, entry)
	}
	return entries
}

func (f *Facts) MarshalJSON() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dst := map[string]any{}
	f.addTo(dst, false)
	return json.Marshal(dst)
}

func (f *Facts) UnmarshalJSON(raw []byte) error {
	var source map[string]any
	if err := json.Unmarshal(raw, &source); err != nil {
		return err
	}
	f.Merge(source)
	return nil
}

func (f *Facts) addModel(model Model) {
	if model == (Model{}) {
		return
	}
	if f.models == nil {
		f.models = make(map[Model]struct{}, 1)
	}
	if _, present := f.models[model]; present {
		return
	}
	if len(f.models) < MaxModelElements {
		f.models[model] = struct{}{}
		return
	}
	furthest := model
	for member := range f.models {
		if preferModel(furthest, member) {
			furthest = member
		}
	}
	if furthest == model {
		return
	}
	delete(f.models, furthest)
	f.models[model] = struct{}{}
}

func ReadModels(value any) []Model {
	var entries []map[string]any
	switch typed := value.(type) {
	case []map[string]any:
		entries = typed[:min(len(typed), MaxArrivingSetElements)]
	case []any:
		typed = typed[:min(len(typed), MaxArrivingSetElements)]
		entries = make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			entry, ok := item.(map[string]any)
			if !ok {
				return nil
			}
			entries = append(entries, entry)
		}
	default:
		return nil
	}
	out := make([]Model, 0, len(entries))
	for _, entry := range entries {
		family, familyOK := approvedEntryValue(entry, a2a.ModelUsageFamily, modelfamily.Families)
		provider, providerOK := approvedEntryValue(entry, a2a.ModelUsageProvider, taxonomy.ProviderLabels)
		servedFrom, servedOK := approvedEntryValue(entry, a2a.ModelUsageServedFrom, taxonomy.ModelTypes)
		if !familyOK || !providerOK || !servedOK {
			continue
		}
		model := Model{Family: family, Provider: provider, ServedFrom: servedFrom}
		if model == (Model{}) {
			continue
		}
		out = append(out, model)
	}
	return out
}

func approvedEntryValue(entry map[string]any, key string, vocabulary []string) (string, bool) {
	raw, present := entry[key]
	if !present {
		return "", true
	}
	value, ok := raw.(string)
	if !ok || !slices.Contains(vocabulary, value) {
		return "", false
	}
	return value, true
}

func addBounded(set map[string]struct{}, value string, vocabulary []string) map[string]struct{} {
	if value == "" || len(set) >= len(vocabulary) {
		return set
	}
	if set == nil {
		set = make(map[string]struct{}, len(vocabulary))
	}
	set[value] = struct{}{}
	return set
}

func (f *Facts) known(key string, value any, vocabulary []string) string {
	s, _ := value.(string)
	if slices.Contains(vocabulary, s) {
		return s
	}
	if s != "" {
		f.logRejected(key, s)
	}
	return ""
}

func (f *Facts) knownList(key string, value any, vocabulary []string) []string {
	values, ok := taxonomy.ParseArrivingStringSet(value, MaxArrivingSetElements)
	if !ok {
		return nil
	}
	out := make([]string, 0, min(len(values), len(vocabulary)))
	for _, v := range values {
		if slices.Contains(vocabulary, v) {
			out = append(out, v)
		} else {
			f.logRejectedLocked(key, v)
		}
	}
	return out
}

func sorted(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

func exceptSet(set map[string]struct{}, exclude string) map[string]struct{} {
	if _, present := set[exclude]; !present {
		return set
	}
	out := make(map[string]struct{}, len(set))
	for value := range set {
		if value != exclude {
			out[value] = struct{}{}
		}
	}
	return out
}
