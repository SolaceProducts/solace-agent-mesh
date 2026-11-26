// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const specJSON = `{"openapi":"3.0.0"}`

func multipartBody(t *testing.T, filename string, parts map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for name, value := range parts {
		if name == "file" {
			fw, err := mw.CreateFormFile(name, filename)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(fw, value)
			continue
		}
		if err := mw.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, mw.FormDataContentType()
}

func (a *apiClient) callMultipart(method, path string, body io.Reader, contentType string, scopes ...string) *httptest.ResponseRecorder {
	a.t.Helper()
	req := httptest.NewRequest(method, platpkg.APIV2Prefix+path, body)
	req.Header.Set("Content-Type", contentType)
	if len(scopes) == 0 {
		scopes = a.scopes
	}
	if len(scopes) > 0 {
		req.Header.Set("X-SAM-Scopes", strings.Join(scopes, ","))
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

func uploadSpec(t *testing.T, app *apiClient, id, filename, content string) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := multipartBody(t, filename, map[string]string{"file": content})
	return app.callMultipart(http.MethodPut, "/connectors/"+id+"/resource", body, contentType)
}

func TestConnectorResource_DownloadServesTheStoredBytes(t *testing.T) {
	app := newConnectorAPI(t)
	id := createConnector(t, app).ID
	expectStatus(t, uploadSpec(t, app, id, "orders.json", specJSON), http.StatusOK)

	rec := app.call(http.MethodGet, "/connectors/"+id+"/resource", nil)
	expectStatus(t, rec, http.StatusOK)
	if got := rec.Body.String(); got != specJSON {
		t.Errorf("body = %q; the spec must not be wrapped in {data}", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename*=UTF-8''orders.json` {
		t.Errorf("Content-Disposition = %q", got)
	}
}

func TestConnectorResource_DownloadContentTypeFollowsTheExtension(t *testing.T) {
	for filename, want := range map[string]string{
		"orders.yaml": "application/yaml",
		"orders.yml":  "application/yaml",
		"orders.txt":  "application/octet-stream",
	} {
		t.Run(filename, func(t *testing.T) {
			app := newConnectorAPI(t)
			id := createConnector(t, app).ID
			expectStatus(t, uploadSpec(t, app, id, filename, "openapi: 3.0.0"), http.StatusOK)

			rec := app.call(http.MethodGet, "/connectors/"+id+"/resource", nil)
			expectStatus(t, rec, http.StatusOK)
			if got := rec.Header().Get("Content-Type"); got != want {
				t.Errorf("Content-Type = %q, want %q", got, want)
			}
		})
	}
}

func TestConnectorResource_AbsentWhenNothingWasUploadedIs404(t *testing.T) {
	app := newConnectorAPI(t)
	openapi := createConnector(t, app).ID

	rec := app.call(http.MethodPost, "/connectors", map[string]any{
		"name": "plugin-thing", "description": "a connector of a type this build does not know",
		"connectorType": unregisteredConnectorType, "connectorSubtype": "generic",
	})
	expectStatus(t, rec, http.StatusCreated)
	other := decodeData[Connector](t, rec).ID

	for name, id := range map[string]string{"an openapi connector with no upload": openapi, "a non-openapi connector": other} {
		t.Run(name, func(t *testing.T) {
			expectStatus(t, app.call(http.MethodGet, "/connectors/"+id+"/resource", nil), http.StatusNotFound)
		})
	}
}

func TestConnectorResource_RoutesAreMounted(t *testing.T) {
	app := newConnectorAPI(t)
	id := createConnector(t, app).ID

	t.Run("GET", func(t *testing.T) {
		decodeError(t, expectStatus404(t, app.call(http.MethodGet, "/connectors/"+id+"/resource", nil)))
	})
	t.Run("PUT", func(t *testing.T) {
		rec := app.call(http.MethodPut, "/connectors/"+id+"/resource", nil)
		expectStatus(t, rec, http.StatusBadRequest)
		decodeError(t, rec)
	})
}

func expectStatus404(t *testing.T, rec *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	expectStatus(t, rec, http.StatusNotFound)
	return rec
}

func TestConnectorResource_UploadStoresTheSpecAndReportsIt(t *testing.T) {
	app := newConnectorAPI(t)
	created := createConnector(t, app)
	if created.HasResource || created.ContentHash != nil {
		t.Fatalf("a new connector must start without a resource: %+v", created)
	}

	rec := uploadSpec(t, app, created.ID, "orders.json", specJSON)
	expectStatus(t, rec, http.StatusOK)
	got := decodeData[Connector](t, rec)
	if !got.HasResource || got.ContentHash == nil || *got.ContentHash == "" {
		t.Errorf("hasResource/contentHash = %v/%v, want the uploaded spec reported", got.HasResource, got.ContentHash)
	}
	if got.ID != created.ID {
		t.Errorf("id = %q, want %q", got.ID, created.ID)
	}

	replaced := decodeData[Connector](t, uploadSpec(t, app, created.ID, "orders.json", `{"openapi":"3.1.0"}`))
	if *replaced.ContentHash == *got.ContentHash {
		t.Error("a replacement must change contentHash")
	}
	dl := app.call(http.MethodGet, "/connectors/"+created.ID+"/resource", nil)
	if dl.Body.String() != `{"openapi":"3.1.0"}` {
		t.Errorf("download = %q, want the replacement", dl.Body.String())
	}
}

func TestConnectorResource_UploadRejectsEveryUnknownPart(t *testing.T) {
	app := newConnectorAPI(t)
	id := createConnector(t, app).ID

	body, contentType := multipartBody(t, "orders.json", map[string]string{"file": specJSON, "notAField": "x", "alsoWrong": "y"})
	rec := app.callMultipart(http.MethodPut, "/connectors/"+id+"/resource", body, contentType)

	expectStatus(t, rec, http.StatusBadRequest)
	details := decodeError(t, rec).ValidationDetails
	for _, name := range []string{"notAField", "alsoWrong"} {
		if _, ok := details[name]; !ok {
			t.Errorf("want %s in validationDetails, got %v; every unknown part must be reported together", name, details)
		}
	}
	if _, ok := details["file"]; ok {
		t.Errorf("file is the one allowed part and must not be reported: %v", details)
	}
	if got := app.call(http.MethodGet, "/connectors/"+id, nil); decodeData[Connector](t, got).HasResource {
		t.Error("a rejected upload must not store the spec")
	}
}

func TestConnectorResource_UploadReportsUnknownPartsAndMissingFileTogether(t *testing.T) {
	app := newConnectorAPI(t)
	id := createConnector(t, app).ID

	body, contentType := multipartBody(t, "", map[string]string{"notAField": "x"})
	rec := app.callMultipart(http.MethodPut, "/connectors/"+id+"/resource", body, contentType)

	expectStatus(t, rec, http.StatusBadRequest)
	details := decodeError(t, rec).ValidationDetails
	if _, ok := details["notAField"]; !ok {
		t.Errorf("want notAField in %v", details)
	}
	if _, ok := details["file"]; !ok {
		t.Errorf("want file in %v: a missing file is reported with the unknown parts, not after them", details)
	}
}

func TestConnectorResource_UploadRejectsANonMultipartBody(t *testing.T) {
	app := newConnectorAPI(t)
	id := createConnector(t, app).ID

	rec := app.call(http.MethodPut, "/connectors/"+id+"/resource", `{"file":"x"}`)
	expectStatus(t, rec, http.StatusBadRequest)
}

func TestConnectorResource_UploadRefusesAConnectorThatIsNotOpenAPI(t *testing.T) {
	app := newConnectorAPI(t)
	rec := app.call(http.MethodPost, "/connectors", map[string]any{
		"name": "plugin-thing", "description": "a connector of a type this build does not know",
		"connectorType": unregisteredConnectorType, "connectorSubtype": "generic",
	})
	expectStatus(t, rec, http.StatusCreated)
	id := decodeData[Connector](t, rec).ID

	rec = uploadSpec(t, app, id, "orders.json", specJSON)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if decodeError(t, rec).ValidationDetails["connectorType"] == nil {
		t.Error("want a connectorType detail saying only api/openapi connectors take a spec")
	}
	expectStatus(t, app.call(http.MethodGet, "/connectors/"+id+"/resource", nil), http.StatusNotFound)
}

func TestConnectorResource_UploadToAMissingConnectorIs404(t *testing.T) {
	app := newConnectorAPI(t)
	expectStatus(t, uploadSpec(t, app, "11111111-1111-4111-8111-111111111111", "orders.json", specJSON), http.StatusNotFound)
}

func TestConnectorResource_DownloadNamesANonASCIIFileForTheBrowser(t *testing.T) {
	const filename = "spécification-日本.json"
	app := newConnectorAPI(t)
	id := createConnector(t, app).ID
	expectStatus(t, uploadSpec(t, app, id, filename, specJSON), http.StatusOK)

	rec := app.call(http.MethodGet, "/connectors/"+id+"/resource", nil)
	expectStatus(t, rec, http.StatusOK)
	header := rec.Header().Get("Content-Disposition")
	for i := range len(header) {
		if header[i] >= 0x80 {
			t.Fatalf("Content-Disposition %q carries a raw non-ASCII byte; it must use the filename* form", header)
		}
	}
	if !strings.Contains(strings.ToLower(header), "filename*=utf-8''") {
		t.Errorf("Content-Disposition %q lacks the RFC 2231 filename* parameter", header)
	}
	disposition, params, err := mime.ParseMediaType(header)
	if err != nil {
		t.Fatalf("Content-Disposition %q does not parse: %v", header, err)
	}
	if disposition != "attachment" || params["filename"] != filename {
		t.Errorf("Content-Disposition %q decodes to %q %q, want attachment %q", header, disposition, params["filename"], filename)
	}
}

func TestConnectorResource_DownloadEncodesAFilenameThatNeedsIt(t *testing.T) {
	for _, filename := range []string{`my "spec".json`, `back\slash.json`, "semi;colon.json", "sp ace.json", "spécification-日本.json"} {
		t.Run(filename, func(t *testing.T) {
			rec := httptest.NewRecorder()
			FileResponse{ContentType: "application/json", Filename: filename, Body: io.NopCloser(strings.NewReader("{}"))}.WriteTo(rec)

			header := rec.Header().Get("Content-Disposition")
			if strings.ToValidUTF8(header, "") != header || strings.IndexFunc(header, func(r rune) bool { return r >= 0x80 }) >= 0 {
				t.Errorf("Content-Disposition %q carries raw non-ASCII text", header)
			}
			_, params, err := mime.ParseMediaType(header)
			if err != nil || params["filename"] != filename {
				t.Errorf("Content-Disposition %q decodes to %q (%v), want %q", header, params["filename"], err, filename)
			}
		})
	}
}

func TestConnectorResource_DownloadFilenameCannotInjectAHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	FileResponse{ContentType: "application/json", Filename: "bad\r\nX-Injected: 1.json", Body: io.NopCloser(strings.NewReader("{}"))}.WriteTo(rec)

	if rec.Code != http.StatusOK || rec.Body.String() != "{}" {
		t.Fatalf("status %d body %q, want the file sent", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Injected"); got != "" {
		t.Errorf("a filename must not inject a header, got %q", got)
	}
	if strings.ContainsAny(rec.Header().Get("Content-Disposition"), "\r\n") {
		t.Errorf("Content-Disposition holds a line break: %q", rec.Header().Get("Content-Disposition"))
	}
}

func TestConnectorResource_Scopes(t *testing.T) {
	app := newConnectorAPI(t)
	id := createConnector(t, app).ID
	expectStatus(t, uploadSpec(t, app, id, "orders.json", specJSON), http.StatusOK)

	body, contentType := multipartBody(t, "orders.json", map[string]string{"file": specJSON})
	reader := app.as(rbac.ScopeConnectorRead)
	expectStatus(t, reader.call(http.MethodGet, "/connectors/"+id+"/resource", nil), http.StatusOK)
	expectStatus(t, reader.callMultipart(http.MethodPut, "/connectors/"+id+"/resource", body, contentType), http.StatusForbidden)

	updater := app.as(rbac.ScopeConnectorUpdate)
	expectStatus(t, updater.call(http.MethodGet, "/connectors/"+id+"/resource", nil), http.StatusForbidden)
}

func TestConnectorResource_RoutesAllowAPIKeys(t *testing.T) {
	core, deps := newTestCore(t)
	var found []string
	for _, rt := range routeTable(deps, core, core.Logger()) {
		if strings.HasSuffix(rt.Path, "/connectors/{id}/resource") {
			if !rt.AllowAPIKey {
				t.Errorf("%s %s must allow API keys: v1 lists it", rt.Method, rt.Path)
			}
			found = append(found, rt.Method)
		}
	}
	slices.Sort(found)
	if !slices.Equal(found, []string{http.MethodGet, http.MethodPut}) {
		t.Errorf("resource routes = %v, want GET and PUT", found)
	}
}

func specHost(t *testing.T, status int, contentType, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func connectorWithSpecURL(t *testing.T, app *apiClient, name, specURL string) string {
	t.Helper()
	id := createNamedConnector(t, app, name).ID
	rec := app.call(http.MethodPatch, "/connectors/"+id, map[string]any{
		"values": map[string]any{"base_url": connectorBaseURL, "specification_url": specURL},
	})
	expectStatus(t, rec, http.StatusOK)
	return id
}

func TestConnectorSpecification_ReadsTheExternalDocumentThroughThePlatform(t *testing.T) {
	app := newConnectorAPI(t)
	srv, hits := specHost(t, http.StatusOK, "application/json", specJSON)
	id := connectorWithSpecURL(t, app, "orders-api", srv.URL+"/openapi.json")
	hits.Store(0)

	rec := app.call(http.MethodGet, "/connectors/"+id+"/specification", nil)
	expectStatus(t, rec, http.StatusOK)

	got := decodeData[ConnectorSpecification](t, rec)
	if got.Content != specJSON {
		t.Errorf("content = %q, want the document the host served", got.Content)
	}
	if got.Format != "json" {
		t.Errorf("format = %q, want json", got.Format)
	}
	if hits.Load() != 1 {
		t.Errorf("host was asked %d times, want 1", hits.Load())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: the document is another host's, read under this caller's authorization", cc)
	}
}

func TestConnectorSpecification_FormatComesFromTheBytesNotTheHost(t *testing.T) {
	app := newConnectorAPI(t)
	srv, _ := specHost(t, http.StatusOK, "application/json", "openapi: 3.1.0\ninfo:\n  title: Pets\npaths: {}\n")
	id := connectorWithSpecURL(t, app, "orders-api", srv.URL+"/openapi")

	rec := app.call(http.MethodGet, "/connectors/"+id+"/specification", nil)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[ConnectorSpecification](t, rec).Format; got != "yaml" {
		t.Errorf("format = %q, want yaml", got)
	}
}

func TestConnectorSpecification_NothingExternalToReadIs404(t *testing.T) {
	app := newConnectorAPI(t)
	withoutURL := createNamedConnector(t, app, "orders-api").ID
	uploaded := createNamedConnector(t, app, "billing-api").ID
	expectStatus(t, uploadSpec(t, app, uploaded, "orders.json", specJSON), http.StatusOK)

	for name, id := range map[string]string{
		"no specification_url at all":          withoutURL,
		"an uploaded specification, so no URL": uploaded,
		"a connector that does not exist":      "019f9b46-77bc-76ba-a494-017487b5f5c4",
	} {
		t.Run(name, func(t *testing.T) {
			expectStatus(t, app.call(http.MethodGet, "/connectors/"+id+"/specification", nil), http.StatusNotFound)
		})
	}
}

func TestConnectorSpecification_UnreadableHostIs502WithoutItsWords(t *testing.T) {
	const hostWords = "upstream exploded at 10.1.2.3"
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"the host refuses":                    {http.StatusInternalServerError, hostWords},
		"the body is not an OpenAPI document": {http.StatusOK, `{"hello":"world"}`},
	} {
		t.Run(name, func(t *testing.T) {
			app := newConnectorAPI(t)
			srv, _ := specHost(t, tc.status, "application/json", tc.body)
			id := connectorWithSpecURL(t, app, "orders-api", srv.URL+"/openapi.json")

			rec := app.call(http.MethodGet, "/connectors/"+id+"/specification", nil)
			expectStatus(t, rec, http.StatusBadGateway)
			if body := rec.Body.String(); strings.Contains(body, hostWords) || strings.Contains(body, srv.URL) {
				t.Errorf("the answer carries the host's words or address: %s", body)
			}
			if msg := decodeError(t, rec).Message; msg != "Unable to read the OpenAPI specification" {
				t.Errorf("message = %q, want the handler's fixed sentence", msg)
			}
		})
	}
}

func TestConnectorSpecification_NeedsTheReadScope(t *testing.T) {
	app := newConnectorAPI(t)
	srv, _ := specHost(t, http.StatusOK, "application/json", specJSON)
	id := connectorWithSpecURL(t, app, "orders-api", srv.URL+"/openapi.json")

	expectStatus(t, app.as(rbac.ScopeAgentBuilderRead).call(http.MethodGet, "/connectors/"+id+"/specification", nil), http.StatusForbidden)
}
