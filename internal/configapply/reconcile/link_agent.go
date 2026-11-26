// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var linkAgentsResource = platformResource{path: "/linkAgents"}

const (
	linkAgentKind         = "linkAgent"
	linkAgentsPlural      = "linkAgents"
	linkAgentSecretsKind  = "linkAgents"
	linkTokenDigestField  = "tokenSha256"
	linkWakeSecretField   = "wake.clientSecret"
	linkWakeHeadersField  = "wake.headers"
	linkAgentErrorFormat  = "linkAgent %q (%s): %w"
	linkTokenDigestRecipe = "issue a token with `sam-link-sdk link-token --out <file>` and set tokenSha256 to the digest it prints"
)

var (
	linkAgentSpecKeys = []string{"entrypoint", "tokenSha256", "enabled", "owner", "team", "peers", "shareUserProfile", "wake"}
	linkAgentWakeKeys = []string{"url", "tokenUrl", "clientId", "clientSecret", "scope", "headers", "timeoutSeconds", "onStart"}

	errLinkTokenDigestToCreate = errors.New("spec.tokenSha256 is required to create a link agent: a file cannot receive the token Agent Mesh would generate and show once, so " + linkTokenDigestRecipe)
)

func init() {
	generic.Register(generic.Kind{
		Name:          linkAgentKind,
		Plural:        linkAgentsPlural,
		Order:         65,
		CreateProto:   linkAgentHashView{},
		UpdateProto:   linkAgentHashView{},
		ResponseProto: linkAgentHashView{},
		NameFromCreate: func(v any) string {
			c, _ := v.(linkAgentHashView)
			return c.Name
		},
		NameFromResponse: func(v any) string {
			r, _ := v.(linkAgentHashView)
			return r.Name
		},
		IDFromResponse: func(v any) string {
			r, _ := v.(linkAgentHashView)
			return r.ID
		},
	})
}

type linkAgentHashView struct {
	ID               string                 `json:"id" cfgapply:"read-only"`
	Name             string                 `json:"name"`
	Description      string                 `json:"description"`
	EntrypointID     string                 `json:"entrypointId"`
	Enabled          bool                   `json:"enabled"`
	Owner            string                 `json:"owner"`
	Team             string                 `json:"team"`
	Peers            []string               `json:"peers"`
	ShareUserProfile bool                   `json:"shareUserProfile"`
	Wake             *linkAgentWakeHashView `json:"wake"`
}

type linkAgentWakeHashView struct {
	URL            string            `json:"url"`
	TokenURL       string            `json:"tokenUrl"`
	ClientID       string            `json:"clientId"`
	Scope          string            `json:"scope"`
	Headers        map[string]string `json:"headers"`
	TimeoutSeconds int               `json:"timeoutSeconds"`
	OnStart        bool              `json:"onStart"`
}

type linkAgentKindPlan struct {
	Kind      string
	Creates   []linkAgentOp
	Updates   []linkAgentOp
	Unchanged []linkAgentOp
	Deletes   []linkAgentOp
}

type linkAgentOp struct {
	Name       string
	Source     string
	Provenance string
	ID         string
	Create     wire.CreateLinkAgentRequest
	Update     wire.UpdateLinkAgentRequest
	Actual     *wire.LinkAgentResponse
}

func (op linkAgentOp) outOfSync() bool {
	if op.Actual == nil {
		return false
	}
	return redeployNeeded(op.Actual.SyncStatus, op.Actual.DeploymentStatus) || op.Actual.DeploymentStatus == entplatform.DisplayStatusDeployFailed
}

func (op linkAgentOp) planRow() planRow {
	r := planRow{name: op.Name, provenance: op.Provenance}
	if op.outOfSync() {
		r.outOfSync = true
		r.lastDeployFailed = op.Actual.DeploymentStatus == entplatform.DisplayStatusDeployFailed
	}
	return r
}

func (op linkAgentOp) redeployResult() linkAgentOpResult {
	return linkAgentOpResult{Op: op, Verb: "redeploy", DeploymentStatus: op.Actual.DeploymentStatus}
}

type linkAgentOpResult struct {
	Op               linkAgentOp
	Verb             string
	Err              error
	DeploymentStatus string
}

func (r linkAgentOpResult) deployRef() deployRef {
	return deployRef{name: r.Op.Name, id: r.Op.ID, status: r.DeploymentStatus}
}

func (r linkAgentOpResult) deployFailure(err error) linkAgentOpResult {
	return linkAgentOpResult{Op: r.Op, Verb: verbDeploy, Err: err}
}

type linkAgentKindOutcome struct {
	Kind        string
	Created     []linkAgentOpResult
	Updated     []linkAgentOpResult
	Deleted     []linkAgentOpResult
	Skipped     []linkAgentOpResult
	OutOfSync   []linkAgentOpResult
	Failures    []linkAgentOpResult
	Deployments []deployResult
}

func (c *Client) ListLinkAgents(ctx context.Context) ([]wire.LinkAgentResponse, error) {
	return listResource[wire.LinkAgentResponse](ctx, c, linkAgentsResource)
}

type linkEntrypoints struct {
	onPlatform map[string]wire.EntrypointResponse
	toCreate   map[string]string
}

func newLinkEntrypoints(actual []wire.EntrypointResponse, plan *gatewayKindPlan) linkEntrypoints {
	refs := linkEntrypoints{onPlatform: make(map[string]wire.EntrypointResponse, len(actual)), toCreate: map[string]string{}}
	for _, e := range actual {
		refs.onPlatform[e.Name] = e
	}
	if plan != nil {
		for _, op := range plan.Creates {
			refs.toCreate[op.Name] = op.Create.EntrypointType
		}
	}
	return refs
}

func (refs linkEntrypoints) idOrNameToCreate(name string) (string, error) {
	if existing, found := refs.onPlatform[name]; found {
		if existing.EntrypointType != platpkg.AgentLinkGatewayType {
			return "", notAnAgentLinkEntrypoint(name, existing.EntrypointType)
		}
		return existing.ID, nil
	}
	if entrypointType, declared := refs.toCreate[name]; declared {
		if entrypointType != platpkg.AgentLinkGatewayType {
			return "", notAnAgentLinkEntrypoint(name, entrypointType)
		}
		return name, nil
	}
	return "", fmt.Errorf("entrypoint %q not found on platform and not declared in the desired manifest", name)
}

func notAnAgentLinkEntrypoint(name, entrypointType string) error {
	return fmt.Errorf("entrypoint %q is of type %q; a link agent connects through an entrypoint of type %q", name, entrypointType, platpkg.AgentLinkGatewayType)
}

func planLinkAgents(ctx context.Context, differ SecretDiffer, desired []resolve.ResolvedResource, actual []wire.LinkAgentResponse, entrypoints linkEntrypoints) (*linkAgentKindPlan, error) {
	k, ok := generic.Lookup(linkAgentKind)
	if !ok {
		return nil, fmt.Errorf("linkAgent kind not registered")
	}
	actualByName := make(map[string]wire.LinkAgentResponse, len(actual))
	for _, a := range actual {
		actualByName[a.Name] = a
	}

	entries := make([]generic.DesiredEntry, 0, len(desired))
	live := make(map[string]wire.CreateLinkAgentRequest, len(desired))
	for _, r := range desired {
		create, err := translateLinkAgent(r, entrypoints)
		if err != nil {
			return nil, err
		}
		if _, onPlatform := actualByName[r.Name]; !onPlatform && create.TokenSHA256 == "" {
			return nil, fmt.Errorf(linkAgentErrorFormat, r.Name, r.SourceFile, errLinkTokenDigestToCreate)
		}
		if _, dup := live[r.Name]; dup {
			return nil, fmt.Errorf("duplicate linkAgent name %q in resolved resources", r.Name)
		}
		live[r.Name] = create
		entries = append(entries, generic.DesiredEntry{
			Name:       r.Name,
			Source:     r.SourceFile,
			Provenance: r.Provenance.String(),
			Create:     linkAgentHashViewFromCreate(create),
		})
	}

	actualVals := make([]reflect.Value, 0, len(actual))
	for _, a := range actual {
		actualVals = append(actualVals, reflect.ValueOf(linkAgentHashViewFromResponse(a)))
	}
	gp, err := generic.Plan(entries, actualVals, k)
	if err != nil {
		return nil, err
	}
	plan := convertLinkAgentPlan(gp, live, actualByName)
	if err := reclassifyLinkAgentRotations(ctx, differ, plan, live); err != nil {
		return nil, err
	}
	return plan, nil
}

func translateLinkAgent(r resolve.ResolvedResource, entrypoints linkEntrypoints) (wire.CreateLinkAgentRequest, error) {
	fail := func(err error) (wire.CreateLinkAgentRequest, error) {
		return wire.CreateLinkAgentRequest{}, fmt.Errorf(linkAgentErrorFormat, r.Name, r.SourceFile, err)
	}
	if err := rejectUnknownLinkAgentKeys(r.Spec); err != nil {
		return fail(err)
	}
	in := make(map[string]any, len(r.Spec)+2)
	maps.Copy(in, r.Spec)
	in["name"] = r.Name
	if r.Description != "" {
		in["description"] = r.Description
	}
	create, err := translate.ForwardLinkAgentV2(in, translate.LinkAgentV2Hooks{})
	if err != nil {
		return fail(err)
	}
	if digestErr := checkLinkTokenDigest(create.TokenSHA256); digestErr != nil {
		return fail(digestErr)
	}
	if create.EntrypointID == "" {
		return fail(fmt.Errorf("spec.entrypoint is required: the name of the %s entrypoint the link agent connects through", platpkg.AgentLinkGatewayType))
	}
	entrypointID, err := entrypoints.idOrNameToCreate(create.EntrypointID)
	if err != nil {
		return fail(fmt.Errorf("entrypoint: %w", err))
	}
	create.EntrypointID = entrypointID
	return create, nil
}

func rejectUnknownLinkAgentKeys(spec map[string]any) error {
	for _, key := range slices.Sorted(maps.Keys(spec)) {
		if !slices.Contains(linkAgentSpecKeys, key) {
			return fmt.Errorf("unknown spec key %q (allowed: %s)", key, strings.Join(linkAgentSpecKeys, ", "))
		}
	}
	wake, isMap := spec["wake"].(map[string]any)
	if raw, set := spec["wake"]; set && raw != nil && !isMap {
		return fmt.Errorf("spec.wake must be a mapping, got %T", raw)
	}
	for _, key := range slices.Sorted(maps.Keys(wake)) {
		if !slices.Contains(linkAgentWakeKeys, key) {
			return fmt.Errorf("unknown spec.wake key %q (allowed: %s)", key, strings.Join(linkAgentWakeKeys, ", "))
		}
	}
	return nil
}

func checkLinkTokenDigest(digest string) error {
	switch {
	case digest == "":
	case !entplatform.IsLinkTokenDigest(digest):
		return fmt.Errorf("spec.tokenSha256 must be the SHA-256 digest of the agent's token, as 64 lowercase hexadecimal characters; %s", linkTokenDigestRecipe)
	case digest == entplatform.DigestOfAnEmptyToken:
		return fmt.Errorf("spec.tokenSha256 is the digest of an empty token, which happens when the variable that held the token was unset; %s", linkTokenDigestRecipe)
	}
	return nil
}

func linkAgentHashViewFromCreate(c wire.CreateLinkAgentRequest) linkAgentHashView {
	view := linkAgentHashView{
		Name:             strings.TrimSpace(c.Name),
		Description:      c.Description,
		EntrypointID:     c.EntrypointID,
		Enabled:          c.Enabled == nil || *c.Enabled,
		Owner:            strings.TrimSpace(c.Owner),
		Team:             strings.TrimSpace(c.Team),
		Peers:            sortedCopy(c.Peers),
		ShareUserProfile: c.ShareUserProfile,
	}
	if w := c.Wake; w != nil {
		view.Wake = &linkAgentWakeHashView{
			URL:            strings.TrimSpace(w.URL),
			TokenURL:       strings.TrimSpace(w.TokenURL),
			ClientID:       strings.TrimSpace(w.ClientID),
			Scope:          strings.TrimSpace(w.Scope),
			Headers:        headerNamesOnly(w.Headers),
			TimeoutSeconds: entplatform.EffectiveLinkWakeTimeout(w.TimeoutSeconds),
			OnStart:        w.OnStart == nil || *w.OnStart,
		}
	}
	return view
}

func linkAgentHashViewFromResponse(r wire.LinkAgentResponse) linkAgentHashView {
	view := linkAgentHashView{
		ID:               r.ID,
		Name:             r.Name,
		Description:      ptrString(r.Description),
		EntrypointID:     r.EntrypointID,
		Enabled:          r.Enabled,
		Owner:            ptrString(r.Owner),
		Team:             ptrString(r.Team),
		Peers:            sortedCopy(r.Peers),
		ShareUserProfile: r.ShareUserProfile,
	}
	if w := r.Wake; w != nil {
		view.Wake = &linkAgentWakeHashView{
			URL:            w.URL,
			TokenURL:       ptrString(w.TokenURL),
			ClientID:       ptrString(w.ClientID),
			Scope:          ptrString(w.Scope),
			Headers:        headerNamesOnly(w.Headers),
			TimeoutSeconds: w.TimeoutSeconds,
			OnStart:        w.OnStart,
		}
	}
	return view
}

func convertLinkAgentPlan(gp *generic.KindPlan, live map[string]wire.CreateLinkAgentRequest, actual map[string]wire.LinkAgentResponse) *linkAgentKindPlan {
	out := &linkAgentKindPlan{Kind: linkAgentKind}
	stored := func(name string) *wire.LinkAgentResponse {
		if a, found := actual[name]; found {
			return &a
		}
		return nil
	}
	for _, op := range gp.Creates {
		out.Creates = append(out.Creates, linkAgentOp{Name: op.Name, Source: op.Source, Provenance: op.Provenance, Create: live[op.Name]})
	}
	for _, op := range gp.Updates {
		out.Updates = append(out.Updates, linkAgentOp{
			Name: op.Name, Source: op.Source, Provenance: op.Provenance, ID: op.ID,
			Create: live[op.Name], Update: linkAgentUpdate(live[op.Name]), Actual: stored(op.Name),
		})
	}
	for _, op := range gp.Unchanged {
		out.Unchanged = append(out.Unchanged, linkAgentOp{Name: op.Name, Source: op.Source, Provenance: op.Provenance, ID: op.ID, Create: live[op.Name], Actual: stored(op.Name)})
	}
	for _, op := range gp.Deletes {
		out.Deletes = append(out.Deletes, linkAgentOp{Name: op.Name, ID: op.ID})
	}
	return out
}

func linkAgentUpdate(create wire.CreateLinkAgentRequest) wire.UpdateLinkAgentRequest {
	update, _ := generic.ToUpdate(create, wire.UpdateLinkAgentRequest{}).(wire.UpdateLinkAgentRequest)
	return update
}

func reclassifyLinkAgentRotations(ctx context.Context, differ SecretDiffer, plan *linkAgentKindPlan, live map[string]wire.CreateLinkAgentRequest) error {
	kept, promoted, err := classifyRotations(ctx, differ, plan.Unchanged, linkAgentKind, rotationClassifier[linkAgentOp]{
		NameOf:       func(op linkAgentOp) string { return op.Name },
		SecretFields: func(op linkAgentOp) []string { return linkAgentWriteOnlyFieldsSet(live[op.Name]) },
		UpdateScope:  rbac.ScopeAgentBuilderUpdate,
		Diff: func(ctx context.Context, op linkAgentOp) (bool, error) {
			return differ.DiffSecrets(ctx, linkAgentSecretsKind, op.ID, linkAgentSecretsDiffForm(live[op.Name]))
		},
		Promote: func(op linkAgentOp) linkAgentOp {
			op.Create, op.Update = live[op.Name], linkAgentUpdate(live[op.Name])
			return op
		},
	})
	plan.Unchanged = kept
	plan.Updates = append(plan.Updates, promoted...)
	return err
}

func linkAgentWriteOnlyFieldsSet(desired wire.CreateLinkAgentRequest) []string {
	var fields []string
	if desired.TokenSHA256 != "" {
		fields = append(fields, linkTokenDigestField)
	}
	if desired.Wake != nil && desired.Wake.ClientSecret != "" {
		fields = append(fields, linkWakeSecretField)
	}
	if desired.Wake != nil && len(desired.Wake.Headers) > 0 {
		fields = append(fields, linkWakeHeadersField)
	}
	return fields
}

func headerNamesOnly(headers map[string]string) map[string]string {
	if headers == nil {
		return nil
	}
	names := make(map[string]string, len(headers))
	for name := range headers {
		names[name] = redactedSentinel
	}
	return names
}

func linkAgentSecretsDiffForm(desired wire.CreateLinkAgentRequest) apiv2.LinkAgentSecretsDiffForm {
	form := apiv2.LinkAgentSecretsDiffForm{TokenSHA256: desired.TokenSHA256}
	if desired.Wake != nil {
		form.Wake = &apiv2.LinkAgentWakeSecretsDiffForm{ClientSecret: desired.Wake.ClientSecret, Headers: desired.Wake.Headers}
	}
	return form
}

func linkAgentPeerErrors(plan *linkAgentKindPlan, meshNames map[string]struct{}, instanceNameOf map[string]string) error {
	var errs []error
	for _, op := range slices.Concat(plan.Creates, plan.Updates, plan.Unchanged) {
		for _, peer := range op.Create.Peers {
			if _, onMesh := meshNames[peer]; onMesh {
				continue
			}
			if instanceName, isDisplayName := instanceNameOf[peer]; isDisplayName {
				errs = append(errs, fmt.Errorf(linkAgentErrorFormat, op.Name, op.Source, displayNamePeerError(peer, instanceName)))
			}
		}
	}
	return errors.Join(errs...)
}

func displayNamePeerError(peer, instanceName string) error {
	if instanceName == "" {
		return fmt.Errorf("peer %q is the name of an agent this apply creates, and a peer names an agent by its instanceName, which Agent Mesh assigns when it creates the agent; apply without this peer, then add the agent's instanceName (agent_ followed by its id with underscores) to peers", peer)
	}
	return fmt.Errorf("peer %q is the name of an agent the platform deploys, and a peer names an agent by its instanceName; write %q in peers instead", peer, instanceName)
}

func applyLinkAgentDeletes(ctx context.Context, c *Client, plan *linkAgentKindPlan, opts ApplyOptions) *linkAgentKindOutcome {
	outcome := &linkAgentKindOutcome{Kind: linkAgentKind}
	for _, op := range plan.Deletes {
		if !opts.Prune {
			outcome.Skipped = append(outcome.Skipped, linkAgentOpResult{Op: op, Verb: "delete"})
			continue
		}
		err := c.callResource(ctx, linkAgentsResource, http.MethodDelete, "/"+url.PathEscape(op.ID), nil, nil)
		res := linkAgentOpResult{Op: op, Verb: "delete", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Deleted = append(outcome.Deleted, res)
		}
	}
	return outcome
}

func applyLinkAgentUpserts(ctx context.Context, c *Client, plan *linkAgentKindPlan, outcome *linkAgentKindOutcome) {
	entrypointIDs := newlyCreatedEntrypointIDs(ctx, c, plan)
	for _, op := range plan.Creates {
		var resp apiv2.LinkAgentWithToken
		resolveErr := entrypointIDs.resolve(&op)
		res := linkAgentOpResult{Op: op, Verb: "create", Err: resolveErr}
		if res.Err == nil {
			res.Err = c.callResource(ctx, linkAgentsResource, http.MethodPost, "", op.Create, &resp)
		}
		if res.Err != nil {
			outcome.Failures = append(outcome.Failures, res)
			continue
		}
		res.Op.ID = resp.ID
		outcome.Created = append(outcome.Created, res)
	}
	for _, op := range plan.Updates {
		var resp wire.LinkAgentResponse
		resolveErr := entrypointIDs.resolve(&op)
		res := linkAgentOpResult{Op: op, Verb: "update", Err: resolveErr}
		if res.Err == nil {
			res.Err = c.callResource(ctx, linkAgentsResource, http.MethodPut, "/"+url.PathEscape(op.ID), op.Update, &resp)
		}
		if res.Err != nil {
			outcome.Failures = append(outcome.Failures, res)
			continue
		}
		res.Op.ID = resp.ID
		res.DeploymentStatus = resp.DeploymentStatus
		outcome.Updated = append(outcome.Updated, res)
	}
	outcome.OutOfSync = outOfSyncRedeploys(plan.Unchanged)
}

type createdEntrypointIDs struct {
	byName  map[string]string
	listErr error
}

func newlyCreatedEntrypointIDs(ctx context.Context, c *Client, plan *linkAgentKindPlan) createdEntrypointIDs {
	awaitsAnEntrypoint := slices.ContainsFunc(slices.Concat(plan.Creates, plan.Updates), func(op linkAgentOp) bool {
		return !looksLikeUUID(op.Create.EntrypointID)
	})
	if !awaitsAnEntrypoint {
		return createdEntrypointIDs{}
	}
	entrypoints, err := c.ListGateways(ctx)
	if err != nil {
		return createdEntrypointIDs{listErr: fmt.Errorf("list entrypoints to resolve the one this apply created: %w", err)}
	}
	ids := createdEntrypointIDs{byName: make(map[string]string, len(entrypoints))}
	for _, e := range entrypoints {
		ids.byName[e.Name] = e.ID
	}
	return ids
}

func (ids createdEntrypointIDs) resolve(op *linkAgentOp) error {
	if looksLikeUUID(op.Create.EntrypointID) {
		return nil
	}
	if ids.listErr != nil {
		return ids.listErr
	}
	id, err := rewritePlaceholderID(op.Create.EntrypointID, ids.byName, linkAgentKind, op.Name, "entrypoint")
	if err != nil {
		return err
	}
	op.Create.EntrypointID, op.Update.EntrypointID = id, id
	return nil
}

func runLinkAgentDeployments(ctx context.Context, d Deployer, progress io.Writer, outcome *linkAgentKindOutcome) {
	if outcome == nil || d == nil {
		return
	}
	results, failures := deployKind(ctx, d, progress, linkAgentKind, outcome.Created, outcome.Updated, outcome.OutOfSync)
	outcome.Deployments = append(outcome.Deployments, results...)
	outcome.Failures = append(outcome.Failures, failures...)
}
