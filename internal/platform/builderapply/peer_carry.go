// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"path/filepath"
	"slices"
	"strings"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func (d *desiredComponent) carryAdditionalConfigurations(merged entplatform.CreateAgentRequest) error {
	if len(d.peerAccess) == 0 && len(d.extraConfig) == 0 {
		d.create, d.planProto = merged, merged
		return nil
	}
	stored := readStoredPeerBlock(merged.AdditionalConfigurations)
	switch {
	case len(d.peerAccess) > 0:
		if err := d.layPeerAccess(&stored); err != nil {
			return err
		}
	case stored.unreadable:
		return refuseCarry(stored.problems, unreadableRemedy)
	}
	if problems := layExtraConfig(stored.blob, d.extraConfig); len(problems) > 0 {
		return refuseCarry(problems, extraConfigRemedy)
	}
	d.warnings = append(d.warnings, divergentCopyWarnings(stored.blob, d.extraConfig)...)
	if written := withoutRemovals(d.extraConfig, extraConfigKeys); len(written) > 0 {
		warnings, err := checkWrittenBounds(merged, written)
		if err != nil {
			return err
		}
		for _, w := range warnings {
			d.warnings = appendOnce(d.warnings, w)
		}
	}
	carried, err := marshalWithoutHTMLEscaping(stored.blob)
	if err != nil {
		return fmt.Errorf("encode additionalConfigurations: %w", err)
	}
	merged.AdditionalConfigurations = carried
	d.create, d.planProto = merged, merged
	if len(stored.blob) == 0 {
		plan := merged
		plan.AdditionalConfigurations = nil
		d.planProto = plan
	}
	return nil
}

func (d *desiredComponent) layPeerAccess(stored *peerBlock) error {
	if len(stored.problems) > 0 {
		return &entplatform.ValidationError{
			Message: "the stored peer-access settings cannot be updated safely: " + strings.Join(stored.problems, "; ") + "; " + stored.remedy(),
			Details: map[string][]string{interAgentCommunicationKey: stored.problems},
		}
	}
	d.storedAllow = stored.allow
	if _, statesList := d.peerAccess[allowListStoredKey]; statesList {
		d.warnings = append(d.warnings, removedPeerWarnings(stored.allow, stringsIn(d.peerAccess[allowListStoredKey]))...)
	}
	for _, key := range slices.Sorted(maps.Keys(d.peerAccess)) {
		stored.block[stored.spelling(key)] = d.peerAccess[key]
	}
	stored.blob[stored.blockKey] = stored.block
	return nil
}

func refuseCarry(problems []string, remedy string) error {
	return &entplatform.ValidationError{
		Message: "the stored additional settings cannot be updated safely: " + strings.Join(problems, "; ") + "; " + remedy,
		Details: map[string][]string{"additionalConfigurations": problems},
	}
}

const (
	peerRemedy = "to go on, rewrite this agent's additionalConfigurations with the agents API (or `sam config pull`, edit, then `sam config apply`; " +
		"both replace the whole blob) so that it holds one interAgentCommunication block with well-formed values and no inter_agent_communication block, " +
		"or leave inter_agent_communication out of this agent's artifact to keep what is stored"
	unreadableRemedy = "to go on, replace this agent's additionalConfigurations with a JSON object using the agents API or `sam config apply`; " +
		"until then no other change to the agent can be saved either"
)

func (pb peerBlock) remedy() string {
	if pb.unreadable {
		return unreadableRemedy
	}
	return peerRemedy
}

func marshalWithoutHTMLEscaping(v any) (json.RawMessage, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(out.Bytes(), "\n"), nil
}

type peerBlock struct {
	blob       map[string]any
	block      map[string]any
	blockKey   string
	allow      []string
	problems   []string
	unreadable bool
}

func (pb peerBlock) spelling(platformKey string) string {
	if pb.blockKey == interAgentCommunicationKey {
		return runtimeSpellingOf(platformKey)
	}
	return platformKey
}

func readStoredPeerBlock(stored json.RawMessage) peerBlock {
	pb := peerBlock{blob: map[string]any{}, block: map[string]any{}, blockKey: interAgentCommunicationStoredKey}
	if len(bytes.TrimSpace(stored)) == 0 {
		return pb
	}
	blob, err := decodeLikeDeploy(stored)
	if err != nil {
		pb.problems = append(pb.problems, "the stored additionalConfigurations is not an object deploy can read, so the agent runs with no additional settings")
		pb.unreadable = true
		return pb
	}
	pb.blob = blob

	camel, hasCamel := blob[interAgentCommunicationStoredKey]
	snake, hasSnake := blob[interAgentCommunicationKey]
	switch {
	case hasCamel && hasSnake:
		pb.problems = append(pb.problems, "both "+interAgentCommunicationStoredKey+" and "+interAgentCommunicationKey+" are stored, and deploy chooses between them by chance")
	case hasCamel:
		pb.readBlock(interAgentCommunicationStoredKey, camel)
	case hasSnake:
		pb.blockKey = interAgentCommunicationKey
		pb.readBlock(interAgentCommunicationKey, snake)
	}
	return pb
}

func decodeLikeDeploy(raw []byte) (map[string]any, error) {
	var asDeploy any
	if err := json.Unmarshal(raw, &asDeploy); err != nil {
		return nil, err
	}
	if _, isObject := asDeploy.(map[string]any); !isObject {
		return nil, errors.New("not an object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var blob map[string]any
	if err := decoder.Decode(&blob); err != nil {
		return nil, err
	}
	return blob, nil
}

func (pb *peerBlock) readBlock(key string, raw any) {
	object, isObject := raw.(map[string]any)
	if !isObject {
		pb.problems = append(pb.problems, fmt.Sprintf("the stored %s is %s, which the runtime reads as allowing every peer", key, describeStored(raw)))
		return
	}
	pb.block = object
	for _, name := range slices.Sorted(maps.Keys(object)) {
		value := object[name]
		known, isOwnSpelling := pb.knownKey(name)
		switch {
		case isOtherSpelling(pb.blockKey, name):
			pb.problems = append(pb.problems, fmt.Sprintf("%s.%s is spelled the way %s spells it", key, name, otherBlockKey(pb.blockKey)))
		case isOwnSpelling && !wellFormedPeerValue(known, value):
			pb.problems = append(pb.problems, fmt.Sprintf("%s.%s is not a well-formed %s (stored: %s)", key, name, peerValueShape(known), describeStoredValue(value)))
		}
	}
	if value, present := object[pb.spelling(allowListStoredKey)]; present {
		pb.allow = stringsIn(value)
	}
}

func (pb peerBlock) knownKey(name string) (peerAccessKey, bool) {
	if pb.blockKey == interAgentCommunicationKey {
		known, ok := peerAccessKeys[name]
		return known, ok
	}
	for _, known := range peerAccessKeys {
		if known.stored == name {
			return known, true
		}
	}
	return peerAccessKey{}, false
}

func isOtherSpelling(blockKey, name string) bool {
	if blockKey == interAgentCommunicationKey {
		return isPlatformSpelledPeerKey(name)
	}
	_, isRuntimeSpelled := peerAccessKeys[name]
	return isRuntimeSpelled
}

func otherBlockKey(blockKey string) string {
	if blockKey == interAgentCommunicationKey {
		return interAgentCommunicationStoredKey
	}
	return interAgentCommunicationKey
}

func describeStored(raw any) string {
	if raw == nil {
		return "null"
	}
	return "not an object"
}

func isPlatformSpelledPeerKey(key string) bool {
	for _, known := range peerAccessKeys {
		if known.stored == key {
			return true
		}
	}
	return false
}

func runtimeSpellingOf(platformKey string) string {
	for runtimeKey, known := range peerAccessKeys {
		if known.stored == platformKey {
			return runtimeKey
		}
	}
	return platformKey
}

func wellFormedPeerValue(key peerAccessKey, value any) bool {
	if key.patterns {
		entries, isList := value.([]any)
		if !isList {
			return false
		}
		for _, entry := range entries {
			if _, isText := entry.(string); !isText {
				return false
			}
		}
		return true
	}
	number, isNumber := value.(json.Number)
	if !isNumber {
		return false
	}
	seconds, ok := new(big.Rat).SetString(number.String())
	return ok && seconds.IsInt() && seconds.Sign() >= 0
}

func peerValueShape(key peerAccessKey) string {
	if key.patterns {
		return "list of peer names"
	}
	return "non-negative whole number of seconds"
}

func describeStoredValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case json.Number:
		const limit = 40
		if text := v.String(); len(text) > limit {
			return text[:limit] + "..."
		}
		return v.String()
	case string:
		return "text"
	case bool:
		return "a boolean"
	case []any:
		for _, entry := range v {
			if _, isText := entry.(string); !isText {
				return "a list holding an entry that is not text"
			}
		}
		return "a list"
	}
	return "an object"
}

func removedPeerWarnings(before, after []string) []string {
	var lost, patterns []string
	for _, entry := range droppedNames(before, after) {
		switch {
		case isGlob(entry):
			patterns = append(patterns, entry)
		case !matchesAnyPattern(after, entry):
			lost = append(lost, entry)
		}
	}
	var out []string
	if len(lost) > 0 {
		out = append(out, fmt.Sprintf("this agent can no longer delegate to %s: it is not in the allow_list this build states", strings.Join(lost, ", ")))
	}
	if len(patterns) > 0 {
		out = append(out, fmt.Sprintf("the allow_list entry %s was removed, so peers only it matched are no longer allowed", strings.Join(patterns, ", ")))
	}
	return out
}

func matchesAnyPattern(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if matched, _ := filepath.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

func checkWrittenBounds(merged entplatform.CreateAgentRequest, written map[string]any) ([]string, error) {
	raw, err := json.Marshal(written)
	if err != nil {
		return nil, fmt.Errorf("agent %q: encode additional settings: %w", merged.Name, err)
	}
	warnings, err := checkExtraConfigBounds(raw, storedInCanonicalSpelling(merged.AdditionalConfigurations))
	return warnings, namedValidationError("", err)
}
