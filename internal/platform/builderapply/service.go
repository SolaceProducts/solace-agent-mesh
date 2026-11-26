// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type Service struct {
	deps    deps
	catalog toolsetCatalog
	logger  *slog.Logger

	features            *features.Registry
	instructionMinChars int
	instructionMaxChars int

	analytics *platpkg.ResourceReporter
}

type Bounds struct {
	AgentInstructionMinChars int
	AgentInstructionMaxChars int
}

func NewService(rd *entplatform.RouteDeps, bounds Bounds, analytics *platpkg.ResourceReporter, logger *slog.Logger) *Service {
	d := deps{
		Agents:         rd.AgentSvc,
		Workflows:      rd.WorkflowSvc,
		Connectors:     rd.ConnectorSvc,
		Gateways:       rd.GatewaySvc,
		A2AAgents:      rd.A2AAgentSvc,
		Skills:         rd.SkillSvc,
		DB:             rd.DB,
		AgentDeploy:    rd.AgentDeploySvc,
		WorkflowDeploy: rd.WorkflowDeploySvc,
	}
	if rd.DB != nil {
		d.Ledger = store.NewBuilderSessionResourceRepo(rd.DB)
		d.AgentDeployments = store.NewAgentDeploymentRepo(rd.DB)
		d.WorkflowDeployments = store.NewWorkflowDeploymentRepo(rd.DB)
	}
	return &Service{
		features:            rd.FeatureRegistry,
		analytics:           analytics,
		instructionMinChars: bounds.AgentInstructionMinChars,
		instructionMaxChars: bounds.AgentInstructionMaxChars,
		deps:                d,
		catalog:             newToolsetCatalog(),
		logger:              logger.With("logger_name", "builderapply"),
	}
}

type kindPlan struct {
	componentKind string
	plan          *generic.KindPlan
	desiredByName map[string]desiredComponent
	inputByName   map[string]BuildComponentInput
	nameByInput   map[string]string
	rotatedField  map[string]string
	renamedFrom   map[string]string
	renamedByID   map[string]struct{}
}

func (s *Service) Plan(ctx context.Context, req ApplyBuildRequest, identity *auth.Subject) ([]kindPlan, *xrefRegistry, error) {
	if len(req.Components) == 0 {
		return nil, newXRefRegistry(), nil
	}
	if err := authorizeReads(identity, req); err != nil {
		return nil, nil, err
	}

	byKind := map[string][]BuildComponentInput{}
	for _, c := range req.Components {
		byKind[c.Kind] = append(byKind[c.Kind], c)
	}

	reg, err := s.seedXRefRegistry(ctx, req)
	if err != nil {
		return nil, nil, err
	}

	var out []kindPlan
	for _, componentKind := range componentKindsInOrder() {
		comps := byKind[componentKind]
		if len(comps) == 0 {
			continue
		}
		kp, err := s.planKind(ctx, componentKind, comps, reg, canUpdateKind(identity, componentKind))
		if err != nil {
			return nil, nil, err
		}
		out = append(out, kp)
	}
	if err := s.rejectUnknownPeers(ctx, identity, out); err != nil {
		return nil, nil, err
	}
	return out, reg, nil
}

func (s *Service) planKind(ctx context.Context, componentKind string, comps []BuildComponentInput, reg *xrefRegistry, mayCompareSecrets bool) (kindPlan, error) {
	k, ok := kindForComponent(componentKind)
	if !ok {
		return kindPlan{}, fmt.Errorf("unknown component kind %q", componentKind)
	}

	desiredByName := make(map[string]desiredComponent, len(comps))
	inputByName := make(map[string]BuildComponentInput, len(comps))
	nameByInput := make(map[string]string, len(comps))
	wantNames := make(map[string]struct{}, len(comps))
	nameByNormalized := make(map[string]string, len(comps))
	legacyNames := legacyClaims{}

	for _, c := range comps {
		d, err := desiredForKind(componentKind, c.Name, c.Content, s.catalog)
		if err != nil {
			return kindPlan{}, submittedArtifactError(err)
		}
		if err := bindAgentReferences(&d, c, reg); err != nil {
			return kindPlan{}, &entplatform.ValidationError{Message: err.Error()}
		}
		name := k.NameFromCreate(d.planProto)
		if name == "" {
			return kindPlan{}, &entplatform.ValidationError{
				Message: fmt.Sprintf("%s %q resolved to an empty name", componentKind, c.Name),
			}
		}
		normalized := normalizedResourceName(name)
		if clash, dup := nameByNormalized[normalized]; dup {
			return kindPlan{}, &entplatform.ValidationError{Message: fmt.Sprintf(
				"%s components %q and %q resolve to %q and %q, which are one name on the platform — it matches ignoring case and spaces; rename one of them",
				displayKind(componentKind), nameByInput[clash], c.Name, clash, name)}
		}
		nameByNormalized[normalized] = name
		desiredByName[name] = d
		inputByName[name] = c
		nameByInput[name] = c.Name
		wantNames[name] = struct{}{}
		legacyNames.claim(d.legacyName, name)
	}

	claimedIDs, err := claimResourceIDs(componentKind, inputByName)
	if err != nil {
		return kindPlan{}, err
	}
	disregarded := disregardedIDs{}
	actual, err := s.actualProtos(ctx, componentKind, wantNames, legacyNames, claimedIDs, disregarded)
	if err != nil {
		return kindPlan{}, fmt.Errorf("read current %s state: %w", componentKind, err)
	}

	actualByName := make(map[string]storedResource, len(actual))
	for _, a := range actual {
		actualByName[k.NameFromResponse(a.proto.Interface())] = a
	}
	hashedStored := map[string]reflect.Value{}
	entries := make([]generic.DesiredEntry, 0, len(comps))
	for _, name := range slices.Sorted(maps.Keys(desiredByName)) {
		d := desiredByName[name]
		stored, hasStored := actualByName[name]
		d.warnings = append(d.warnings, disregarded.warnings(name, displayKind(componentKind), hasStored)...)
		if hasStored {
			if immutableErr := rejectImmutableEdits(componentKind, nameByInput[name], d, stored); immutableErr != nil {
				return kindPlan{}, immutableErr
			}
			if preserveErr := preserveUnauthoredFields(componentKind, &d, stored); preserveErr != nil {
				return kindPlan{}, submittedArtifactError(fmt.Errorf("%s %q: %w", componentKind, nameByInput[name], preserveErr))
			}
		}
		d.warnings = append(d.warnings, missingRequiredConnectorFieldWarnings(d, hasStored)...)
		if hasStored && componentKind == kindAgent {
			hashedStored[name] = hashAgentSecretsApart(&d, stored)
		}
		desiredByName[name] = d
		entries = append(entries, generic.DesiredEntry{
			Name:   name,
			Source: inputByName[name].SourceFilename,
			Create: d.planProto,
		})
	}
	slices.SortFunc(entries, func(a, b generic.DesiredEntry) int { return strings.Compare(a.Name, b.Name) })

	protos := make([]reflect.Value, 0, len(actual))
	for _, a := range actual {
		if hashed, ok := hashedStored[k.NameFromResponse(a.proto.Interface())]; ok {
			protos = append(protos, hashed)
			continue
		}
		protos = append(protos, a.proto)
	}
	plan, err := generic.Plan(entries, protos, k)
	if err != nil {
		return kindPlan{}, err
	}
	if len(plan.Deletes) != 0 {
		return kindPlan{}, fmt.Errorf(
			"internal error: planning %s produced %d delete(s); this endpoint must never remove a platform resource",
			componentKind, len(plan.Deletes))
	}

	renamedFrom, renamedByID := renamesIn(actualByName)

	kp := kindPlan{
		renamedFrom:   renamedFrom,
		renamedByID:   renamedByID,
		componentKind: componentKind,
		plan:          plan,
		desiredByName: desiredByName,
		inputByName:   inputByName,
		nameByInput:   nameByInput,
	}
	s.reclassifySecretRotations(ctx, &kp, mayCompareSecrets)
	reclassifyRenames(&kp)
	reclassifySkillSetChanges(&kp)
	return kp, nil
}

func hashAgentSecretsApart(d *desiredComponent, stored storedResource) reflect.Value {
	create, isCreate := d.create.(entplatform.CreateAgentRequest)
	view, isView := d.planProto.(entplatform.CreateAgentRequest)
	actual, isActual := stored.raw.(entplatform.AgentResponse)
	if !isCreate || !isView || !isActual {
		return stored.proto
	}
	create.ToolsetConfigs = keepStoredOverToolsetPlaceholders(create.ToolsetConfigs, actual.ToolsetConfigs)
	d.create = create
	d.suppliedToolsetSecrets = suppliedToolsetSecretPaths(create.ToolsetConfigs, actual.ToolsetConfigs)
	hashedView, hashedActual := hashedAgentViews(view, actual)
	d.planProto = hashedView
	return reflect.ValueOf(hashedActual)
}

func renamesIn(actualByName map[string]storedResource) (renamedFrom map[string]string, renamedByID map[string]struct{}) {
	renamedFrom = map[string]string{}
	renamedByID = map[string]struct{}{}
	for name, stored := range actualByName {
		if stored.storedName != "" {
			renamedFrom[name] = stored.storedName
		}
		if stored.pairedByID {
			renamedByID[name] = struct{}{}
		}
	}
	return renamedFrom, renamedByID
}

func submittedArtifactError(err error) *entplatform.ValidationError {
	out := &entplatform.ValidationError{Message: err.Error()}
	if ve, ok := errors.AsType[*entplatform.ValidationError](err); ok {
		out.Field = ve.Field
		out.Details = ve.Details
	}
	return out
}

func reclassifyRenames(kp *kindPlan) {
	if len(kp.renamedFrom) == 0 {
		return
	}
	kept := kp.plan.Unchanged[:0]
	for _, op := range kp.plan.Unchanged {
		if _, renamed := kp.renamedFrom[op.Name]; renamed {
			kp.plan.Updates = append(kp.plan.Updates, op)
			continue
		}
		kept = append(kept, op)
	}
	kp.plan.Unchanged = kept
}

func reclassifySkillSetChanges(kp *kindPlan) {
	kept := kp.plan.Unchanged[:0]
	for _, op := range kp.plan.Unchanged {
		if d, ok := kp.desiredByName[op.Name]; ok && d.skillSetChanged {
			kp.plan.Updates = append(kp.plan.Updates, op)
			continue
		}
		kept = append(kept, op)
	}
	kp.plan.Unchanged = kept
}

func rejectImmutableEdits(componentKind, componentName string, d desiredComponent, stored storedResource) error {
	for _, field := range immutableFields[componentKind] {
		want, wantOK := field.desired(d.create)
		got, gotOK := field.actual(stored.raw)
		if !wantOK || !gotOK {
			continue
		}
		if want == "" || want == got {
			continue
		}
		return &entplatform.ValidationError{Message: fmt.Sprintf(
			"%s %q is stored with %s %q and that cannot be changed; create a new %s instead",
			displayKind(componentKind), componentName, field.label, got, displayKind(componentKind))}
	}
	return nil
}

func displayKind(componentKind string) string {
	if componentKind == kindGateway {
		return "entrypoint"
	}
	return componentKind
}

type immutableField struct {
	label   string
	desired func(create any) (string, bool)
	actual  func(raw any) (string, bool)
}

var immutableFields = map[string][]immutableField{
	kindConnector: {
		{
			label:   "type",
			desired: func(c any) (string, bool) { v, ok := c.(entplatform.CreateConnectorRequest); return v.Type, ok },
			actual:  func(r any) (string, bool) { v, ok := r.(entplatform.ConnectorResponse); return v.Type, ok },
		},
		{
			label:   "subtype",
			desired: func(c any) (string, bool) { v, ok := c.(entplatform.CreateConnectorRequest); return v.Subtype, ok },
			actual:  func(r any) (string, bool) { v, ok := r.(entplatform.ConnectorResponse); return v.Subtype, ok },
		},
	},
	kindGateway: {
		{
			label:   "type",
			desired: func(c any) (string, bool) { v, ok := c.(entplatform.CreateGatewayRequest); return v.Type, ok },
			actual:  func(r any) (string, bool) { v, ok := r.(entplatform.GatewayResponse); return v.Type, ok },
		},
	},
}

type storedResource struct {
	proto      reflect.Value
	raw        any
	storedName string
	pairedByID bool
}

func (s *Service) actualProtos(ctx context.Context, componentKind string, want map[string]struct{}, legacy legacyClaims, claims idClaims, disregarded disregardedIDs) ([]storedResource, error) {
	desiredName := make(map[string]string, len(want))
	for name := range want {
		desiredName[normalizedResourceName(name)] = name
	}
	p := pairing{desiredName: desiredName, claims: claims, disregarded: disregarded}

	switch componentKind {
	case kindConnector:
		return s.actualConnectors(ctx, p)
	case kindA2AAgent:
		return s.actualA2AAgents(ctx, p)
	case kindAgent:
		return s.actualAgents(ctx, p)
	case kindWorkflow:
		return s.actualWorkflows(ctx, p, legacy)
	case kindGateway:
		return s.actualGateways(ctx, p)
	default:
		return nil, fmt.Errorf("no reader for component kind %q", componentKind)
	}
}

type idClaims map[string]string

var renamableInPlace = map[string]bool{kindAgent: true, kindWorkflow: true}

func claimResourceIDs(componentKind string, inputByName map[string]BuildComponentInput) (idClaims, error) {
	claims := idClaims{}
	if !renamableInPlace[componentKind] {
		return claims, nil
	}
	for _, name := range slices.Sorted(maps.Keys(inputByName)) {
		id := strings.ToLower(inputByName[name].ResourceID)
		if id == "" {
			continue
		}
		if other, taken := claims[id]; taken {
			return nil, &entplatform.ValidationError{Message: fmt.Sprintf(
				"%s components %q and %q carry the same resourceId, so they name one stored resource; give each component its own id or drop the stale one",
				displayKind(componentKind), inputByName[other].Name, inputByName[name].Name)}
		}
		claims[id] = name
	}
	return claims, nil
}

type storedKey struct{ id, name string }

type pairing struct {
	desiredName map[string]string
	claims      idClaims
	disregarded disregardedIDs
}

type disregardedIDs map[string]disregardedID

type disregardedID struct {
	storedName string
}

func (d disregardedIDs) warnings(name, kind string, paired bool) []string {
	miss, disregarded := d[name]
	if !disregarded {
		return nil
	}
	head := fmt.Sprintf("the build manifest records an id for this component that no stored %s has", kind)
	if miss.storedName != "" {
		head = fmt.Sprintf("the build manifest records the id of the stored %s %q for this component", kind, miss.storedName)
	}
	if paired {
		return []string{fmt.Sprintf("%s, which is not used: the stored %s that carries this name is written instead", head, kind)}
	}
	return []string{fmt.Sprintf("%s, which is not used: a new %s is created", head, kind)}
}

type pairs struct {
	desired map[string]string
	byID    map[string]bool
}

func matchStored[T any](p pairing, list []T, key func(T) storedKey) pairs {
	out := pairs{desired: map[string]string{}, byID: map[string]bool{}}
	held := map[string]bool{}
	for _, item := range list {
		k := key(item)
		if name, ok := p.desiredName[normalizedResourceName(k.name)]; ok {
			out.desired[k.id] = name
			held[name] = true
		}
	}
	for _, item := range list {
		k := key(item)
		if _, paired := out.desired[k.id]; paired {
			continue
		}
		name, claimed := p.claims[strings.ToLower(k.id)]
		if !claimed || held[name] {
			continue
		}
		out.desired[k.id] = name
		out.byID[k.id] = true
		held[name] = true
	}
	p.noteDisregardedIDs(out, storedKeys(list, key))
	return out
}

func storedKeys[T any](list []T, key func(T) storedKey) map[string]storedKey {
	byID := make(map[string]storedKey, len(list))
	for _, item := range list {
		k := key(item)
		byID[strings.ToLower(k.id)] = k
	}
	return byID
}

func (p pairing) noteDisregardedIDs(out pairs, stored map[string]storedKey) {
	for id, name := range p.claims {
		recorded, listed := stored[id]
		if listed && out.desired[recorded.id] == name {
			continue
		}
		p.disregarded[name] = disregardedID{storedName: recorded.name}
	}
}

func renamedResource(proto any, raw any, storedName, desired string, byID bool) storedResource {
	r := storedResource{proto: reflect.ValueOf(proto), raw: raw, pairedByID: byID}
	if storedName != desired {
		r.storedName = storedName
	}
	return r
}

func (s *Service) actualConnectors(ctx context.Context, p pairing) ([]storedResource, error) {
	list, err := s.deps.listConnectors(ctx)
	if err != nil {
		return nil, err
	}
	m := matchStored(p, list, func(a entplatform.ConnectorResponse) storedKey { return storedKey{a.ID, a.Name} })
	var out []storedResource
	for _, a := range list {
		desired, ok := m.desired[a.ID]
		if !ok {
			continue
		}
		proto := a
		proto.Name = desired
		out = append(out, renamedResource(proto, a, a.Name, desired, m.byID[a.ID]))
	}
	return out, nil
}

func (s *Service) actualA2AAgents(ctx context.Context, p pairing) ([]storedResource, error) {
	list, err := s.deps.listA2AAgents(ctx)
	if err != nil {
		return nil, err
	}
	m := matchStored(p, list, func(a entplatform.RemoteAgentResponse) storedKey { return storedKey{a.ID, a.Name} })
	var out []storedResource
	for _, a := range list {
		if desired, ok := m.desired[a.ID]; ok {
			view := a2aViewFromResponse(a)
			view.Name = desired
			out = append(out, renamedResource(view, a, a.Name, desired, m.byID[a.ID]))
		}
	}
	return out, nil
}

func (s *Service) actualAgents(ctx context.Context, p pairing) ([]storedResource, error) {
	list, err := s.deps.listAgents(ctx)
	if err != nil {
		return nil, err
	}
	m := matchStored(p, list, func(a entplatform.AgentResponse) storedKey { return storedKey{a.ID, a.Name} })
	var out []storedResource
	for _, a := range list {
		desired, ok := m.desired[a.ID]
		if !ok {
			continue
		}
		full, getErr := s.deps.Agents.GetByID(ctx, a.ID)
		if getErr != nil {
			return nil, fmt.Errorf("read agent %q: %w", a.Name, getErr)
		}
		storedName := full.Name
		full.Name = desired
		out = append(out, renamedResource(*full, *full, storedName, desired, m.byID[a.ID]))
	}
	return out, nil
}

func (s *Service) actualGateways(ctx context.Context, p pairing) ([]storedResource, error) {
	list, err := s.deps.listGateways(ctx)
	if err != nil {
		return nil, err
	}
	m := matchStored(p, list, func(a entplatform.GatewayResponse) storedKey { return storedKey{a.ID, a.Name} })
	var out []storedResource
	for _, a := range list {
		desired, ok := m.desired[a.ID]
		if !ok {
			continue
		}
		proto := a
		proto.Name = desired
		out = append(out, renamedResource(proto, a, a.Name, desired, m.byID[a.ID]))
	}
	return out, nil
}

func (s *Service) actualWorkflows(ctx context.Context, p pairing, legacy legacyClaims) ([]storedResource, error) {
	list, err := s.deps.listWorkflows(ctx)
	if err != nil {
		return nil, err
	}
	var out []storedResource
	paired := map[string]bool{}
	used := map[string]bool{}
	m := matchStored(p, list, func(a entplatform.WorkflowResponse) storedKey { return storedKey{a.ID, a.Name} })
	for _, a := range list {
		desired, collides := m.desired[a.ID]
		if !collides {
			continue
		}
		renamed := ""
		if a.Name != desired {
			renamed = a.Name
		}
		out = append(out, storedResource{
			proto:      reflect.ValueOf(newWorkflowHashView(a.ID, desired, a.Description, a.AppConfig)),
			raw:        a,
			storedName: renamed,
			pairedByID: m.byID[a.ID],
		})
		paired[desired] = true
		used[a.ID] = true
	}
	for _, a := range list {
		if used[a.ID] {
			continue
		}
		owner := workflowAgentName(a.AppConfig)
		if paired[owner] || !slices.Contains(legacy[a.Name], owner) {
			continue
		}
		out = append(out, storedResource{
			proto:      reflect.ValueOf(newWorkflowHashView(a.ID, owner, a.Description, a.AppConfig)),
			raw:        a,
			storedName: a.Name,
		})
		paired[owner] = true
	}
	return out, nil
}

type legacyClaims map[string][]string

func (c legacyClaims) claim(legacyName, desired string) {
	if legacyName != "" {
		c[legacyName] = append(c[legacyName], desired)
	}
}

func (kp kindPlan) operations(status string) []BuildOperationResult {
	out := make([]BuildOperationResult, 0, len(kp.plan.Creates)+len(kp.plan.Updates)+len(kp.plan.Unchanged))
	for _, op := range kp.plan.Creates {
		out = append(out, kp.row(op, actionCreate, status))
	}
	for _, op := range kp.plan.Updates {
		out = append(out, kp.row(op, actionUpdate, status))
	}
	for _, op := range kp.plan.Unchanged {
		out = append(out, kp.row(op, actionUnchanged, status))
	}
	return out
}

func (kp kindPlan) row(op generic.Op, action, status string) BuildOperationResult {
	reported := op.Name
	if sent, ok := kp.nameByInput[op.Name]; ok && sent != "" {
		reported = sent
	}
	r := BuildOperationResult{
		Kind:           kp.componentKind,
		Name:           reported,
		ResolvedName:   op.Name,
		SourceFilename: kp.inputByName[op.Name].SourceFilename,
		Action:         action,
		Status:         status,
		ResourceID:     op.ID,
	}
	if d, ok := kp.desiredByName[op.Name]; ok {
		r.Warnings = d.warnings
		r.ChangesDeclined = d.declined
	}
	if action == actionUpdate {
		r.ChangedFields = changedFields(op)
		if field, rotated := kp.rotatedField[op.Name]; rotated && !slices.Contains(r.ChangedFields, field) {
			r.ChangedFields = append(r.ChangedFields, field)
			slices.Sort(r.ChangedFields)
		}
		if d, ok := kp.desiredByName[op.Name]; ok && d.skillSetChanged &&
			!slices.Contains(r.ChangedFields, "skillIds") {
			r.ChangedFields = append(r.ChangedFields, "skillIds")
			slices.Sort(r.ChangedFields)
		}
		if from, renamed := kp.renamedFrom[op.Name]; renamed {
			if !slices.Contains(r.ChangedFields, "name") {
				r.ChangedFields = append(r.ChangedFields, "name")
				slices.Sort(r.ChangedFields)
			}
			_, byID := kp.renamedByID[op.Name]
			r.RenamedFrom = from
			r.Warnings = append(slices.Clone(r.Warnings), renameWarning(from, op.Name, byID))
		}
	}
	return r
}

func renameWarning(from, to string, byID bool) string {
	if byID {
		return fmt.Sprintf("renames %q to %q in place; the build manifest records the platform id of the stored resource, so it keeps its id and its deployment", from, to)
	}
	if normalizedResourceName(from) == normalizedResourceName(to) {
		return fmt.Sprintf("renames %q to %q; the platform matches names ignoring case and spaces, so this is the same resource", from, to)
	}
	return fmt.Sprintf("renames %q to %q; the workflow was stored under its previous name before agent_name took precedence, so this is the same workflow", from, to)
}

func changedFields(op generic.Op) []string {
	if !op.Create.IsValid() || !op.Actual.IsValid() {
		return nil
	}
	redacted := generic.RedactedKeysFromSecretField(op.Actual.Interface())
	desired := generic.ProjectForHash(op.Create.Interface(), redacted)
	actual := generic.ProjectForHash(op.Actual.Interface(), redacted)

	out := make([]string, 0, len(desired))
	for tag, want := range desired {
		got, onBothSides := actual[tag]
		if !onBothSides {
			continue
		}
		if !sameProjectedValue(want, got) {
			out = append(out, tag)
		}
	}
	slices.Sort(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func sameProjectedValue(a, b any) bool {
	rawA, errA := json.Marshal(a)
	rawB, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return reflect.DeepEqual(a, b)
	}
	return bytes.Equal(rawA, rawB)
}

func jsonFieldName(tag string) string {
	name, _, _ := strings.Cut(tag, ",")
	return name
}

func normalizedResourceName(name string) string {
	return store.NormalizeName(name)
}
