// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type ReferencedUserFiles map[string]int

func LatestArtifactVersion(ctx context.Context, artSvc *artifact.ScopedService, userID, sessionID, filename string) (int, error) {
	versions, err := artSvc.Inner.ListVersions(ctx, artSvc.Resolve("", userID, sessionID, filename))
	if err != nil || len(versions) == 0 {
		return -1, err
	}
	return versions[len(versions)-1], nil
}

func (r ReferencedUserFiles) Has(filename string, version int) bool {
	referenced, ok := r[filename]
	return ok && referenced == version
}

var ErrUnreadableTasks = errors.New("chat tasks could not be read")

const (
	artifactURIPrefix            = "artifact://"
	maxUnreadableTaskIDsReported = 5
)

const ForkedFromOwnerKey = "forkedFromOwnerId"

var forkMarkerKeys = []string{
	ForkedFromOwnerKey, "forkedFrom", "forkedFromSessionId",
	"forked_from_owner_id", "forked_from", "forked_from_session_id",
}

func IsForkMarkerKey(key string) bool {
	return slices.Contains(forkMarkerKeys, key)
}

var artifactMarkerPattern = regexp.MustCompile(`«{1,3}artifact(?:_return)?:([^»]+)»{1,3}`)

func UserScopedFilesReferencedBy(tasks []*store.ChatTask, owner string) (ReferencedUserFiles, error) {
	refs := ReferencedUserFiles{}
	var unreadable []string
	for _, task := range tasks {
		if !artifact.SameOwner(task.UserID, owner) {
			continue
		}
		copiedByFork, err := isForkCopy(task)
		if err != nil {
			unreadable = append(unreadable, task.ID)
			continue
		}
		if copiedByFork {
			continue
		}
		if err := refs.collectFromBubbles(task.MessageBubbles); err != nil {
			unreadable = append(unreadable, task.ID)
		}
	}
	if len(unreadable) > 0 {
		listed := unreadable[:min(len(unreadable), maxUnreadableTaskIDsReported)]
		return refs, fmt.Errorf("%w: %d task(s), first: %s", ErrUnreadableTasks, len(unreadable), strings.Join(listed, ","))
	}
	return refs, nil
}

func isForkCopy(task *store.ChatTask) (bool, error) {
	if task.TaskMetadata == nil || *task.TaskMetadata == "" {
		return false, nil
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(*task.TaskMetadata), &metadata); err != nil {
		return false, err
	}
	for key := range metadata {
		if IsForkMarkerKey(key) {
			return true, nil
		}
	}
	return false, nil
}

func PreserveForkMarkers(existing, incoming *string) *string {
	if existing == nil || *existing == "" {
		return incoming
	}
	var stored map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*existing), &stored); err != nil {
		return incoming
	}
	markers := map[string]json.RawMessage{}
	for key, value := range stored {
		if IsForkMarkerKey(key) {
			markers[key] = value
		}
	}
	if len(markers) == 0 {
		return incoming
	}
	merged := map[string]json.RawMessage{}
	if incoming != nil && *incoming != "" {
		if err := json.Unmarshal([]byte(*incoming), &merged); err != nil || merged == nil {
			merged = map[string]json.RawMessage{}
		}
	}
	for key, value := range markers {
		merged[key] = value
	}
	var encoded strings.Builder
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(merged); err != nil {
		return incoming
	}
	preserved := strings.TrimSuffix(encoded.String(), "\n")
	return &preserved
}

func (r ReferencedUserFiles) collectFromBubbles(raw string) error {
	if raw == "" {
		return nil
	}
	var bubbles []map[string]any
	if err := json.Unmarshal([]byte(raw), &bubbles); err != nil {
		return err
	}
	for _, bubble := range bubbles {
		r.collectFromBubble(bubble)
	}
	return nil
}

func (r ReferencedUserFiles) collectFromBubble(bubble map[string]any) {
	r.collectFromText(textOf(bubble["text"]))
	for _, part := range objectsOf(bubble["parts"]) {
		switch textOf(part["kind"]) {
		case "text":
			r.collectFromText(textOf(part["text"]))
		case "artifact", "file":
			r.collectFromFiles(objectsOf(part["file"]))
		}
	}
	r.collectFromFiles(objectsOf(bubble["files"]))
	for _, attached := range objectsOf(bubble["attachedArtifacts"]) {
		r.addURI(textOf(attached["uri"]))
	}
	for _, notification := range objectsOf(bubble["artifactNotification"]) {
		r.addReference(textOf(notification["name"]), versionOf(notification["version"]))
	}
}

func (r ReferencedUserFiles) collectFromFiles(files []map[string]any) {
	for _, file := range files {
		r.addURI(textOf(file["uri"]))
	}
}

func (r ReferencedUserFiles) collectFromText(text string) {
	for _, match := range artifactMarkerPattern.FindAllStringSubmatch(text, -1) {
		name, version := artifact.SplitVersionRef(strings.TrimSpace(match[1]))
		r.addReference(name, version)
	}
}

func (r ReferencedUserFiles) addURI(uri string) {
	if strings.HasPrefix(uri, artifactURIPrefix) {
		name, version := artifactURINameAndVersion(uri)
		r.addReference(name, version)
	}
}

func (r ReferencedUserFiles) addReference(name string, version *int) {
	name = strings.TrimSpace(name)
	stripped, userScoped := artifact.ResolveFilename(name)
	if !userScoped || stripped == "" || version == nil || *version < 0 {
		return
	}
	if current, known := r[name]; !known || *version > current {
		r[name] = *version
	}
}

func versionOf(v any) *int {
	number, ok := v.(float64)
	if !ok || number < 0 || number != float64(int(number)) {
		return nil
	}
	version := int(number)
	return &version
}

func textOf(v any) string {
	text, _ := v.(string)
	return text
}

func objectsOf(v any) []map[string]any {
	switch value := v.(type) {
	case map[string]any:
		return []map[string]any{value}
	case []any:
		objects := make([]map[string]any, 0, len(value))
		for _, element := range value {
			if object, ok := element.(map[string]any); ok {
				objects = append(objects, object)
			}
		}
		return objects
	}
	return nil
}

func artifactURINameAndVersion(uri string) (string, *int) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", nil
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	name, err := url.PathUnescape(segments[len(segments)-1])
	if err != nil {
		return "", nil
	}
	var version *int
	if parsedVersion, convErr := strconv.Atoi(parsed.Query().Get("version")); convErr == nil {
		version = &parsedVersion
	}
	return name, version
}
