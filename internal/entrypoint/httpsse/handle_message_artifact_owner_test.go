// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	artifactRefRefusalLogMsg = "submit message: artifact reference belongs to another user"
	artifactRefAccessLogMsg  = "submit message: check access to artifact reference"
	aliceSecretFilename      = "alice-secret-report.txt"
	carolOwner               = "carol"
	messageStreamRoute       = "/api/v1/message:stream"
)

type uriCase struct{ name, uri string }

func artifactURI(owner, session, filename string) string {
	return "artifact://test-ns/" + owner + "/" + session + "/" + filename + "?version=0"
}

func filePart(uri string) map[string]any {
	return map[string]any{"kind": "file", "file": map[string]any{"name": "attached", "uri": uri}}
}

func textPart(text string) map[string]any {
	return map[string]any{"kind": "text", "text": text}
}

func (f *messageOwnerFixture) postParts(route, userID string, parts ...map[string]any) *httptest.ResponseRecorder {
	return f.postPartsCtx(context.Background(), route, userID, parts...)
}

func (f *messageOwnerFixture) postPartsCtx(ctx context.Context, route, userID string, parts ...map[string]any) *httptest.ResponseRecorder {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "req1", "params": map[string]any{"message": map[string]any{
		"kind": "message", "role": "user", "parts": parts,
		"metadata": map[string]any{"agent_name": ownerTestAgent},
	}}})
	if err != nil {
		panic(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, route, bytes.NewReader(body)).WithContext(ctx)
	f.mux.ServeHTTP(rec, asUser(req, userID))
	return rec
}

func (f *messageOwnerFixture) withProjects(t *testing.T, owner string) *store.Project {
	t.Helper()
	repo := store.NewProjectRepo(f.core.Store())
	f.handler.projectRepo = repo
	project := &store.Project{Name: "shared project", UserID: owner}
	if err := repo.Create(t.Context(), project); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return project
}

func closedProjectRepo(t *testing.T) *store.ProjectRepo {
	t.Helper()
	db, err := store.Open(testTemplate.Clone(t), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err = db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	return store.NewProjectRepo(db)
}

func (f *messageOwnerFixture) withArtifacts(t *testing.T) *artifact.ScopedService {
	t.Helper()
	flags := features.NewRegistry()
	if err := features.LoadFlags(flags); err != nil {
		t.Fatalf("load flags: %v", err)
	}
	arts := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "test-ns"}
	f.core.SetServices(runtime.NewRuntime(f.brk, nil, runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(flags), runtime.WithArtifacts(arts)).Services())
	return arts
}

func assertArtifactRefused(t *testing.T, f *messageOwnerFixture, q broker.Queue, rec *httptest.ResponseRecorder, trackedBefore int) {
	t.Helper()
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
	rpcErr := decodeRPCError(t, rec)
	if rpcErr == nil || rpcErr.Message != artifactNotFoundMsg || rpcErr.Code != codeInvalidParams {
		t.Fatalf("error = %+v, want code %d message %q", rpcErr, codeInvalidParams, artifactNotFoundMsg)
	}
	if data, _ := rpcErr.Data.(map[string]any); data["traceId"] == nil {
		t.Errorf("refusal must carry the trace id like every other refused submit: %+v", rpcErr.Data)
	}
	if got := f.sse.totalTasks(); got != trackedBefore {
		t.Errorf("a refused submit left %d tasks registered in the SSE manager, want %d", got, trackedBefore)
	}
	if msg := publishedTask(t, f.brk, q, 200*time.Millisecond); msg != nil {
		t.Errorf("a task naming another user's artifact was published: %s", msg.Payload)
	}
}

func assertArtifactAccepted(t *testing.T, f *messageOwnerFixture, q broker.Queue, rec *httptest.ResponseRecorder) *broker.Message {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	published := publishedTask(t, f.brk, q, 2*time.Second)
	if published == nil {
		t.Fatal("task was not published")
	}
	return published
}

var traceIDInBody = regexp.MustCompile(`"traceId":"[^"]*"`)

func TestHandleMessage_ArtifactReference_ByOwnership(t *testing.T) {
	aliceSession := idgen.New()
	bobSession := idgen.New()

	for _, route := range messageRoutes {
		t.Run(route, func(t *testing.T) {
			f, q := newMessageOwnerFixture(t)
			arts := f.withArtifacts(t)
			if _, err := arts.Inner.Save(t.Context(), arts.Resolve("", createOwnerAlice, aliceSession, aliceSecretFilename), []byte("alice-only"), "text/plain"); err != nil {
				t.Fatalf("save alice's artifact: %v", err)
			}

			t.Run("another user's artifact is refused and nothing is submitted", func(t *testing.T) {
				trackedBefore := f.sse.totalTasks()
				rec := f.postParts(route, createOwnerBob, textPart("summarise this"), filePart(artifactURI(createOwnerAlice, aliceSession, aliceSecretFilename)))
				assertArtifactRefused(t, f, q, rec, trackedBefore)
				for _, leaked := range []string{aliceSecretFilename, aliceSession, createOwnerAlice, "alice-only"} {
					if strings.Contains(rec.Body.String(), leaked) {
						t.Errorf("refusal body carries %q: %s", leaked, rec.Body.String())
					}
				}
			})

			t.Run("the refusal is byte for byte the same whether or not the artifact exists", func(t *testing.T) {
				existing := f.postParts(route, createOwnerBob, filePart(artifactURI(createOwnerAlice, aliceSession, aliceSecretFilename)))
				missing := f.postParts(route, createOwnerBob, filePart(artifactURI(createOwnerAlice, aliceSession, "missing.txt")))
				strip := func(rec *httptest.ResponseRecorder) string {
					return traceIDInBody.ReplaceAllString(rec.Body.String(), `"traceId":""`)
				}
				if existing.Code != missing.Code || strip(existing) != strip(missing) {
					t.Errorf("answers differ: %d %s vs %d %s", existing.Code, existing.Body.String(), missing.Code, missing.Body.String())
				}
			})

			t.Run("the caller's own artifact is accepted and passed on unchanged", func(t *testing.T) {
				uri := artifactURI(createOwnerBob, bobSession, "my-notes.txt")
				published := assertArtifactAccepted(t, f, q, f.postParts(route, createOwnerBob, textPart("summarise this"), filePart(uri)))
				if !strings.Contains(string(published.Payload), uri) {
					t.Errorf("the agent did not receive the URI as sent: %s", published.Payload)
				}
			})

			t.Run("a message without a file part is accepted", func(t *testing.T) {
				assertArtifactAccepted(t, f, q, f.postParts(route, createOwnerBob, textPart("hello")))
			})
		})
	}
}

func TestHandleMessage_ArtifactReference_OwnerIsReadTheWayTheAgentReadsIt(t *testing.T) {
	f, q := newMessageOwnerFixture(t)

	refused := []uriCase{
		{"owner is the third segment from the end", "artifact://test-ns/" + createOwnerBob + "/x/" + createOwnerAlice + "/s/report.txt"},
		{"upper-case scheme", "ARTIFACT://test-ns/alice/s/report.txt"},
		{"percent-encoded owner", "artifact://test-ns/%61lice/s/report.txt"},
		{"slash inside the owner segment", "artifact://test-ns/" + createOwnerAlice + "%2F" + createOwnerBob + "/s/report.txt"},
		{"no version", "artifact://test-ns/alice/s/report.txt"},
		{"version zero", "artifact://test-ns/alice/s/report.txt?version=0"},
		{"large version", "artifact://test-ns/alice/s/report.txt?version=999999"},
		{"fragment", "artifact://test-ns/alice/s/report.txt?version=0#bob"},
		{"query names the caller", "artifact://test-ns/alice/s/report.txt?version=0&owner=bob"},
		{"user-scoped name of another user", "artifact://test-ns/alice/s/user:report.txt?version=0"},
		{"encoded user-scoped name of another user", "artifact://test-ns/alice/s/user%3Areport.txt?version=0"},
	}
	for _, tc := range refused {
		t.Run("refused: "+tc.name, func(t *testing.T) {
			trackedBefore := f.sse.totalTasks()
			assertArtifactRefused(t, f, q, f.postParts(messageStreamRoute, createOwnerBob, filePart(tc.uri)), trackedBefore)
		})
	}

	accepted := []uriCase{
		{"extra leading segments leave the caller as the owner", "artifact://test-ns/" + createOwnerAlice + "/s/" + createOwnerBob + "/x/report.txt"},
		{"percent-encoded caller", "artifact://test-ns/%62ob/s/report.txt"},
		{"escaped slash in the last segment", "artifact://test-ns/" + createOwnerBob + "/" + createOwnerAlice + "/s%2Freport.txt"},
		{"the caller's own user-scoped name", "artifact://test-ns/bob/s/user:report.txt?version=2"},
		{"shorthand without owner", "artifact://report.txt:1"},
		{"malformed version", "artifact://test-ns/alice/s/report.txt?version=abc"},
		{"too few segments", "artifact://test-ns/alice/report.txt"},
		{"not an artifact", "https://example.com/alice/s/report.txt"},
	}
	for _, tc := range accepted {
		t.Run("passed on: "+tc.name, func(t *testing.T) {
			assertArtifactAccepted(t, f, q, f.postParts(messageStreamRoute, createOwnerBob, filePart(tc.uri)))
		})
	}
}

func TestHandleMessage_ArtifactReference_AnyForeignPartRefusesTheMessage(t *testing.T) {
	f, q := newMessageOwnerFixture(t)
	own := filePart(artifactURI(createOwnerBob, idgen.New(), "mine.txt"))
	foreign := filePart(artifactURI(createOwnerAlice, idgen.New(), aliceSecretFilename))
	mislabelled := map[string]any{"kind": "text", "text": "x", "file": map[string]any{"uri": artifactURI(createOwnerAlice, idgen.New(), aliceSecretFilename)}}

	for _, tc := range []struct {
		name  string
		parts []map[string]any
	}{
		{"second of two", []map[string]any{own, foreign}},
		{"first of two", []map[string]any{foreign, own}},
		{"kind text", []map[string]any{mislabelled}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trackedBefore := f.sse.totalTasks()
			assertArtifactRefused(t, f, q, f.postParts("/api/v1/message:send", createOwnerBob, tc.parts...), trackedBefore)
		})
	}
}

func TestHandleMessage_ArtifactReference_ProjectShare(t *testing.T) {
	setup := func(t *testing.T, sharing *mockProjectSharingService) (*messageOwnerFixture, broker.Queue, *store.Project) {
		t.Helper()
		f, q := newMessageOwnerFixture(t)
		project := f.withProjects(t, createOwnerAlice)
		if sharing != nil {
			f.core.SetProjectSharingService(sharing)
		}
		return f, q, project
	}
	projectScope := func(p *store.Project) string { return projectSessionPrefix + p.ID }

	t.Run("a member of the shared project is accepted, whichever way the reference is spelled", func(t *testing.T) {
		sharing := &mockProjectSharingService{hasAccess: true}
		f, q, project := setup(t, sharing)
		for _, tc := range []uriCase{
			{"with a version", artifactURI(createOwnerAlice, projectScope(project), "spec.txt")},
			{"without a version", "artifact://test-ns/alice/" + projectScope(project) + "/spec.txt"},
			{"encoded file name", artifactURI(createOwnerAlice, projectScope(project), "my%20spec.txt")},
			{"encoded owner", "artifact://test-ns/%61lice/" + projectScope(project) + "/spec.txt?version=3"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				assertArtifactAccepted(t, f, q, f.postParts(messageStreamRoute, createOwnerBob, filePart(tc.uri)))
			})
		}
		if sharing.lastViewAccessProjectID != project.ID || sharing.lastViewAccessUserID != createOwnerBob {
			t.Errorf("access asked for project %q user %q, want %q %q", sharing.lastViewAccessProjectID, sharing.lastViewAccessUserID, project.ID, createOwnerBob)
		}
	})

	t.Run("the share lookup is made for the caller as identified, the share table lower-cases it", func(t *testing.T) {
		sharing := &mockProjectSharingService{hasAccess: true}
		f, q, project := setup(t, sharing)
		assertArtifactAccepted(t, f, q, f.postParts(messageStreamRoute, "Bob@Example.COM", filePart(artifactURI(createOwnerAlice, projectScope(project), "spec.txt"))))
		if sharing.lastViewAccessUserID != "Bob@Example.COM" {
			t.Errorf("access asked for user %q, want the caller unchanged: normalising the id before it is compared with the project's owner is how a different id gets taken for the owner", sharing.lastViewAccessUserID)
		}
	})

	refusedWithShare := func(t *testing.T, cases func(p *store.Project) []uriCase) {
		t.Helper()
		sharing := &mockProjectSharingService{hasAccess: true}
		f, q, project := setup(t, sharing)
		for _, tc := range cases(project) {
			t.Run(tc.name, func(t *testing.T) {
				trackedBefore := f.sse.totalTasks()
				assertArtifactRefused(t, f, q, f.postParts(messageStreamRoute, createOwnerBob, filePart(tc.uri)), trackedBefore)
			})
		}
	}

	t.Run("a share does not open anything but the project's own files", func(t *testing.T) {
		refusedWithShare(t, func(p *store.Project) []uriCase {
			return []uriCase{
				{"another session of the owner", artifactURI(createOwnerAlice, idgen.New(), "private.txt")},
				{"another user's files filed under the project id", artifactURI(carolOwner, projectScope(p), "spec.txt")},
				{"a user-scoped name of the owner", artifactURI(createOwnerAlice, projectScope(p), "user:notes.md")},
				{"a user-scoped name of another user", artifactURI(carolOwner, projectScope(p), "user:notes.md")},
				{"an encoded user-scoped name", artifactURI(carolOwner, projectScope(p), "user%3Anotes.md")},
				{"an encoded u of a user-scoped name", artifactURI(createOwnerAlice, projectScope(p), "%75ser:notes.md")},
				{"the project's owner spelled in another case", artifactURI("ALICE", projectScope(p), "spec.txt")},
				{"the project's id without the project- prefix", artifactURI(createOwnerAlice, p.ID, "spec.txt")},
			}
		})
	})

	t.Run("a project id that is not the canonical one is refused without asking the sharing service", func(t *testing.T) {
		sharing := &mockProjectSharingService{hasAccess: true}
		f, q, project := setup(t, sharing)
		for _, tc := range []uriCase{
			{"upper case", artifactURI(createOwnerAlice, projectSessionPrefix+strings.ToUpper(project.ID), "spec.txt")},
			{"braced", artifactURI(createOwnerAlice, projectSessionPrefix+"%7B"+project.ID+"%7D", "spec.txt")},
			{"unhyphenated", artifactURI(createOwnerAlice, projectSessionPrefix+strings.ReplaceAll(project.ID, "-", ""), "spec.txt")},
			{"urn", artifactURI(createOwnerAlice, projectSessionPrefix+"urn:uuid:"+project.ID, "spec.txt")},
			{"not an id", artifactURI(createOwnerAlice, projectSessionPrefix+"zzz", "spec.txt")},
			{"empty", artifactURI(createOwnerAlice, projectSessionPrefix, "spec.txt")},
		} {
			t.Run(tc.name, func(t *testing.T) {
				trackedBefore := f.sse.totalTasks()
				assertArtifactRefused(t, f, q, f.postParts(messageStreamRoute, createOwnerBob, filePart(tc.uri)), trackedBefore)
			})
		}
		if sharing.lastViewAccessProjectID != "" {
			t.Errorf("the sharing service was asked about %q", sharing.lastViewAccessProjectID)
		}
	})

	t.Run("a project id that is not the canonical one never reaches the project table", func(t *testing.T) {
		f, q, project := setup(t, &mockProjectSharingService{hasAccess: true})
		f.handler.projectRepo = closedProjectRepo(t)
		trackedBefore := f.sse.totalTasks()

		assertArtifactRefused(t, f, q, f.postParts(messageStreamRoute, createOwnerBob, filePart(artifactURI(createOwnerAlice, projectSessionPrefix+"zzz", "spec.txt"))), trackedBefore)

		rec := f.postParts(messageStreamRoute, createOwnerBob, filePart(artifactURI(createOwnerAlice, projectScope(project), "spec.txt")))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("a canonical id with the project table down: status = %d, want 500", rec.Code)
		}
	})

	t.Run("a project that does not exist is refused", func(t *testing.T) {
		f, q, _ := setup(t, &mockProjectSharingService{hasAccess: true})
		trackedBefore := f.sse.totalTasks()
		assertArtifactRefused(t, f, q, f.postParts(messageStreamRoute, createOwnerBob, filePart(artifactURI(createOwnerAlice, projectSessionPrefix+"0194f4a0-0000-7000-8000-000000000000", "spec.txt"))), trackedBefore)
	})

	t.Run("someone the project is not shared with is refused", func(t *testing.T) {
		f, q, project := setup(t, &mockProjectSharingService{hasAccess: false})
		trackedBefore := f.sse.totalTasks()
		assertArtifactRefused(t, f, q, f.postParts(messageStreamRoute, createOwnerBob, filePart(artifactURI(createOwnerAlice, projectScope(project), "spec.txt"))), trackedBefore)
	})

	t.Run("with no sharing service a project artifact of another user is refused", func(t *testing.T) {
		f, q, project := setup(t, nil)
		trackedBefore := f.sse.totalTasks()
		assertArtifactRefused(t, f, q, f.postParts(messageStreamRoute, createOwnerBob, filePart(artifactURI(createOwnerAlice, projectScope(project), "spec.txt"))), trackedBefore)
	})

	t.Run("with no project table a project artifact of another user is refused", func(t *testing.T) {
		f, q := newMessageOwnerFixture(t)
		f.core.SetProjectSharingService(&mockProjectSharingService{hasAccess: true})
		trackedBefore := f.sse.totalTasks()
		assertArtifactRefused(t, f, q, f.postParts(messageStreamRoute, createOwnerBob, filePart(artifactURI(createOwnerAlice, projectSessionPrefix+idgen.New(), "spec.txt"))), trackedBefore)
	})

	t.Run("the project's owner may reference their own project files", func(t *testing.T) {
		f, q, project := setup(t, nil)
		assertArtifactAccepted(t, f, q, f.postParts(messageStreamRoute, createOwnerAlice, filePart(artifactURI(createOwnerAlice, projectScope(project), "spec.txt"))))
	})

	t.Run("a failing share lookup is a server error, not a refusal or an acceptance", func(t *testing.T) {
		f, q, project := setup(t, &mockProjectSharingService{viewAccessErr: errors.New("sharing backend down")})
		trackedBefore := f.sse.totalTasks()

		rec := f.postParts(messageStreamRoute, createOwnerBob, filePart(artifactURI(createOwnerAlice, projectScope(project), "spec.txt")))

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
		}
		if rpcErr := decodeRPCError(t, rec); rpcErr == nil || rpcErr.Code != codeInternalError || rpcErr.Message != submitFailedMsg {
			t.Errorf("error = %+v, want an internal error", rpcErr)
		}
		if got := f.sse.totalTasks(); got != trackedBefore {
			t.Errorf("a failed submit left %d tasks registered, want %d", got, trackedBefore)
		}
		if msg := publishedTask(t, f.brk, q, 200*time.Millisecond); msg != nil {
			t.Errorf("a task was published although the access check failed: %s", msg.Payload)
		}
		lines := logLinesMatching(f.logs, artifactRefAccessLogMsg)
		if len(lines) != 1 || !strings.Contains(lines[0], `"level":"ERROR"`) {
			t.Fatalf("access-check failure logged as %v, want one ERROR line", lines)
		}
		for _, leaked := range []string{"artifact://", "spec.txt", "version=", project.ID} {
			if strings.Contains(lines[0], leaked) {
				t.Errorf("error log carries %q: %s", leaked, lines[0])
			}
		}
	})

	t.Run("a client that has gone away is not answered and not logged as an error", func(t *testing.T) {
		f, q, project := setup(t, &mockProjectSharingService{viewAccessErr: errors.New("sharing backend down")})
		gone, cancel := context.WithCancel(context.Background())
		cancel()
		trackedBefore := f.sse.totalTasks()

		rec := f.postPartsCtx(gone, messageStreamRoute, createOwnerBob, filePart(artifactURI(createOwnerAlice, projectScope(project), "spec.txt")))

		if rec.Body.Len() != 0 {
			t.Errorf("a client that was gone was answered: %d %s", rec.Code, rec.Body.String())
		}
		if got := f.sse.totalTasks(); got != trackedBefore {
			t.Errorf("tasks registered = %d, want %d", got, trackedBefore)
		}
		if msg := publishedTask(t, f.brk, q, 200*time.Millisecond); msg != nil {
			t.Errorf("a task was published for a client that was gone: %s", msg.Payload)
		}
		if lines := logLinesMatching(f.logs, artifactRefAccessLogMsg); len(lines) > 0 {
			t.Errorf("a client disconnect was logged as a failure: %v", lines)
		}
	})
}

func TestHandleMessage_ArtifactReference_OwnerComparisonMatchesByURI(t *testing.T) {
	f, q := newMessageOwnerFixture(t)
	assertArtifactAccepted(t, f, q, f.postParts(messageStreamRoute, "Bob", filePart(artifactURI("bob", idgen.New(), "mine.txt"))))
}

func logLinesMatching(logs *safeBuffer, msg string) []string {
	var lines []string
	for l := range strings.SplitSeq(logs.String(), "\n") {
		if strings.Contains(l, msg) {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestHandleMessage_ArtifactReferenceRefusalIsLoggedAtWarn(t *testing.T) {
	f, _ := newMessageOwnerFixture(t)
	typedSession := "typed-by-the-caller-" + strings.Repeat("x", 200)

	f.postParts(messageStreamRoute, createOwnerBob, textPart(ownerTestPrompt), filePart(artifactURI(createOwnerAlice, typedSession, aliceSecretFilename)))

	lines := logLinesMatching(f.logs, artifactRefRefusalLogMsg)
	if len(lines) != 1 {
		t.Fatalf("refusal logged %d times under %q, want once: %s", len(lines), artifactRefRefusalLogMsg, f.logs.String())
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &line); err != nil {
		t.Fatalf("decode log line %q: %v", lines[0], err)
	}
	if line["level"] != "WARN" {
		t.Errorf("refusal logged at %v, want WARN", line["level"])
	}
	if line["userID"] != createOwnerBob || line["traceID"] == nil {
		t.Errorf("refusal log lacks ids: %v", line)
	}
	for _, leaked := range []string{"artifact://", aliceSecretFilename, ownerTestPrompt, "version=", "typed-by-the-caller", createOwnerAlice} {
		if strings.Contains(lines[0], leaked) {
			t.Errorf("refusal log carries %q: %s", leaked, lines[0])
		}
	}
}

func TestMessageArtifactReference_ThroughTheComposedHandler(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: ownerTestAgent, URL: "solace:test"})
	brk := core.Services().Broker()
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", ownerTestAgent)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	send := func(userID, uri string) *httptest.ResponseRecorder {
		raw, marshalErr := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "req1", "params": map[string]any{"message": map[string]any{
			"kind": "message", "role": "user",
			"parts":    []map[string]any{textPart(ownerTestPrompt), filePart(uri)},
			"metadata": map[string]any{"agent_name": ownerTestAgent},
		}}})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		req := httptest.NewRequest(http.MethodPost, messageStreamRoute, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-SAM-User-ID", userID)
		rec := httptest.NewRecorder()
		adapter.Handler().ServeHTTP(rec, req)
		return rec
	}

	aliceURI := artifactURI(createOwnerAlice, idgen.New(), aliceSecretFilename)

	if rec := send(createOwnerBob, aliceURI); rec.Code != http.StatusNotFound {
		t.Fatalf("bob naming alice's artifact: status %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
	if msg := publishedTask(t, brk, q, 200*time.Millisecond); msg != nil {
		t.Fatalf("bob's task naming alice's artifact was published: %s", msg.Payload)
	}
	if rec := send(createOwnerAlice, aliceURI); rec.Code != http.StatusOK {
		t.Fatalf("alice naming her own artifact: status %d, body=%s", rec.Code, rec.Body.String())
	}
	if publishedTask(t, brk, q, 2*time.Second) == nil {
		t.Fatal("alice's task was not published")
	}
	if rec := send(createOwnerBob, artifactURI(createOwnerBob, idgen.New(), "mine.txt")); rec.Code != http.StatusOK {
		t.Fatalf("bob naming his own artifact: status %d, body=%s", rec.Code, rec.Body.String())
	}
	if publishedTask(t, brk, q, 2*time.Second) == nil {
		t.Fatal("bob's task naming his own artifact was not published")
	}
}
