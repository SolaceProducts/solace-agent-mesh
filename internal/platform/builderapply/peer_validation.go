// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

type peerClaim struct {
	agent, component string
	allow, deny      []string
	storedAllow      map[string]struct{}
}

func peerClaims(plans []kindPlan) []peerClaim {
	var out []peerClaim
	for _, kp := range plans {
		if kp.componentKind != kindAgent {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(kp.desiredByName)) {
			d := kp.desiredByName[name]
			claim := peerClaim{
				agent: name, component: cmp.Or(kp.nameByInput[name], name),
				allow: stringsIn(d.peerAccess[allowListStoredKey]), deny: stringsIn(d.peerAccess[denyListStoredKey]),
				storedAllow: setOf(d.storedAllow),
			}
			if len(claim.allow)+len(claim.deny) > 0 {
				out = append(out, claim)
			}
		}
	}
	return out
}

func setOf(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

func stringsIn(v any) []string {
	entries, _ := v.([]any)
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func isGlob(pattern string) bool { return strings.ContainsAny(pattern, globMeta) }

func validPattern(pattern string) bool {
	for i := 0; i < len(pattern); {
		switch pattern[i] {
		case '\\':
			if i+1 >= len(pattern) {
				return false
			}
			i += 2
		case '[':
			end := classEnd(pattern, i)
			if end < 0 {
				return false
			}
			if _, err := filepath.Match(pattern[i:end+1], "x"); err != nil {
				return false
			}
			i = end + 1
		default:
			i++
		}
	}
	return true
}

func classEnd(pattern string, start int) int {
	i := start + 1
	if i < len(pattern) && pattern[i] == '^' {
		i++
	}
	for ; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case ']':
			return i
		}
	}
	return -1
}

type peerNames struct {
	inBuild      map[string]struct{}
	onPlatform   map[string]map[string]struct{}
	platformRead bool
	readFailed   bool
}

func (n peerNames) known() map[string]struct{} {
	all := maps.Clone(n.inBuild)
	for name := range n.onPlatform {
		all[name] = struct{}{}
	}
	return all
}

func (n *peerNames) dropRenamedAway(plans []kindPlan) {
	for _, kp := range plans {
		for _, was := range kp.renamedFrom {
			delete(n.onPlatform[was], kp.componentKind)
			if len(n.onPlatform[was]) == 0 {
				delete(n.onPlatform, was)
			}
		}
	}
}

func buildPeerNames(plans []kindPlan) map[string]struct{} {
	names := map[string]struct{}{}
	for _, kp := range plans {
		if kp.componentKind == kindAgent || kp.componentKind == kindA2AAgent || kp.componentKind == kindWorkflow {
			for name := range kp.desiredByName {
				names[name] = struct{}{}
			}
		}
	}
	return names
}

type peerFinding struct {
	errors    []string
	warnings  []string
	unchecked bool
}

func (s *Service) rejectUnknownPeers(ctx context.Context, identity *auth.Subject, plans []kindPlan) error {
	claims := peerClaims(plans)
	if len(claims) == 0 {
		return nil
	}
	names := peerNames{inBuild: buildPeerNames(plans)}
	mandatory, optional := peerLookupNeeds(claims, names.inBuild)
	if mandatory || optional {
		if err := s.readPlatformPeerNames(ctx, identity, &names, mandatory); err != nil {
			return err
		}
		names.dropRenamedAway(plans)
	}

	findings := map[string]peerFinding{}
	for _, claim := range claims {
		findings[claim.agent] = checkPeerClaim(claim, names)
	}
	return applyPeerFindings(plans, claims, findings)
}

func peerLookupNeeds(claims []peerClaim, inBuild map[string]struct{}) (mandatory, optional bool) {
	for _, claim := range claims {
		for _, pattern := range claim.allow {
			switch {
			case !validPattern(pattern) || pattern == matchEverything:
			case isGlob(pattern):
				optional = optional || !matchesAny(pattern, inBuild)
			case inSet(inBuild, pattern):
			case inSet(claim.storedAllow, pattern):
				optional = true
			default:
				mandatory = true
			}
		}
		for _, pattern := range claim.deny {
			if validPattern(pattern) && !isGlob(pattern) && !inSet(inBuild, pattern) {
				optional = true
			}
		}
	}
	return mandatory, optional
}

func inSet(set map[string]struct{}, name string) bool {
	_, ok := set[name]
	return ok
}

func checkPeerClaim(claim peerClaim, names peerNames) peerFinding {
	var f peerFinding
	known := names.known()
	for _, pattern := range claim.deny {
		switch {
		case !validPattern(pattern):
			f.errors = append(f.errors, fmt.Sprintf("%s.deny_list entry %q is not a valid pattern (a [ class that never closes, an empty class, or a trailing backslash)", interAgentCommunicationKey, pattern))
		case !isGlob(pattern) && !inSet(known, pattern):
			if names.platformRead {
				f.warnings = append(f.warnings, fmt.Sprintf("%s.deny_list entry %q names no agent in this build or on the platform, so it denies nothing; a misspelt name leaves that agent reachable", interAgentCommunicationKey, pattern))
			} else {
				f.unchecked = true
			}
		}
	}
	for _, pattern := range claim.allow {
		switch {
		case !validPattern(pattern):
			f.errors = append(f.errors, fmt.Sprintf("%s.allow_list entry %q is not a valid pattern (a [ class that never closes, an empty class, or a trailing backslash)", interAgentCommunicationKey, pattern))
		case pattern == matchEverything:
		case isGlob(pattern):
			switch {
			case matchesAny(pattern, known):
			case names.platformRead:
				f.warnings = append(f.warnings, fmt.Sprintf("%s.allow_list pattern %q matches no agent in this build or on the platform, so it allows nothing yet", interAgentCommunicationKey, pattern))
			default:
				f.unchecked = true
			}
		case inSet(known, pattern):
		case inSet(claim.storedAllow, pattern):
			if names.platformRead {
				f.warnings = append(f.warnings, staleStoredPeerMessage(pattern, known))
			} else {
				f.unchecked = true
			}
		default:
			f.errors = append(f.errors, unknownPeerMessage(pattern, known))
		}
	}
	if f.unchecked {
		why := "because it could not be read for this caller"
		if names.readFailed {
			why = "because reading it failed"
		}
		f.warnings = append(f.warnings, "some peer names were not checked against the platform, "+why+"; a misspelt name deploys and reaches nothing")
	}
	return f
}

func matchesAny(pattern string, names map[string]struct{}) bool {
	for name := range names {
		if matched, _ := filepath.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

func staleStoredPeerMessage(name string, known map[string]struct{}) string {
	msg := fmt.Sprintf("%s.allow_list entry %q names no agent in this build or on the platform; it was already stored and was kept", interAgentCommunicationKey, name)
	if lookalike := caseInsensitiveMatch(name, known); lookalike != "" {
		msg += fmt.Sprintf("; did you mean %q?", lookalike)
	}
	return msg
}

func unknownPeerMessage(name string, known map[string]struct{}) string {
	msg := fmt.Sprintf("%s.allow_list entry %q is not an agent in this build or on the platform", interAgentCommunicationKey, name)
	switch lookalike := caseInsensitiveMatch(name, known); {
	case lookalike != "":
		msg += fmt.Sprintf("; did you mean %q?", lookalike)
	case len(known) > 0:
		names := slices.Sorted(maps.Keys(known))
		if len(names) > suggestedNameLimit {
			names = names[:suggestedNameLimit]
		}
		msg += "; known names include " + strings.Join(quoteAll(names), ", ")
	}
	return msg + `; an agent this platform does not manage can be reached with a pattern such as "name*"`
}

func caseInsensitiveMatch(name string, known map[string]struct{}) string {
	var found string
	for candidate := range known {
		if !strings.EqualFold(candidate, name) {
			continue
		}
		if found != "" {
			return ""
		}
		found = candidate
	}
	return found
}

func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = fmt.Sprintf("%q", n)
	}
	return out
}

func applyPeerFindings(plans []kindPlan, claims []peerClaim, findings map[string]peerFinding) error {
	details := map[string][]string{}
	var summary []string
	for _, claim := range claims {
		f := findings[claim.agent]
		if len(f.errors) == 0 {
			continue
		}
		details["agent "+claim.component] = f.errors
		for _, msg := range f.errors {
			summary = append(summary, fmt.Sprintf("agent %q: %s", claim.component, msg))
		}
	}
	if len(summary) > 0 {
		return &entplatform.ValidationError{Message: strings.Join(summary, "; "), Details: details}
	}
	for i, kp := range plans {
		if kp.componentKind != kindAgent {
			continue
		}
		for name, d := range kp.desiredByName {
			d.warnings = append(d.warnings, findings[name].warnings...)
			plans[i].desiredByName[name] = d
		}
	}
	return nil
}

func (s *Service) readPlatformPeerNames(ctx context.Context, identity *auth.Subject, names *peerNames, required bool) error {
	if missing := missingPeerLookupScopes(identity); len(missing) > 0 {
		if !required {
			return nil
		}
		return &entplatform.ForbiddenError{
			Message: "Insufficient permissions: checking " + interAgentCommunicationKey + ".allow_list names requires " + strings.Join(missing, ", "),
		}
	}
	onPlatform, err := s.listPeerNames(ctx)
	if err != nil {
		if required {
			return err
		}
		s.logger.Warn("builder could not read platform names to check a peer list", "error", err)
		names.readFailed = true
		return nil
	}
	names.onPlatform, names.platformRead = onPlatform, true
	return nil
}

var peerNameKinds = []string{kindAgent, kindA2AAgent, kindWorkflow}

func missingPeerLookupScopes(identity *auth.Subject) []string {
	var missing []string
	for _, kind := range peerNameKinds {
		scope, _ := readScopeForKind(kind)
		if (identity == nil || !identity.HasScope(scope)) && !slices.Contains(missing, scope) {
			missing = append(missing, scope)
		}
	}
	return missing
}

func (s *Service) listPeerNames(ctx context.Context) (map[string]map[string]struct{}, error) {
	names := map[string]map[string]struct{}{}
	hold := func(name, kind string) {
		if names[name] == nil {
			names[name] = map[string]struct{}{}
		}
		names[name][kind] = struct{}{}
	}
	agents, err := s.deps.listAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	for _, a := range agents {
		hold(a.Name, kindAgent)
	}
	remote, err := s.deps.listA2AAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("list remote agents: %w", err)
	}
	for _, a := range remote {
		hold(a.Name, kindA2AAgent)
	}
	workflows, err := s.deps.listWorkflows(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	for _, w := range workflows {
		hold(w.Name, kindWorkflow)
	}
	return names, nil
}
