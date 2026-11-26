// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"context"
	"net/http"
	"path"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func connectorResourceRoutes(h *connectorHandler) []Route {
	return []Route{
		{Method: http.MethodGet, Path: connectorPath + "/resource", Scope: rbac.ScopeConnectorRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "downloadConnectorResource", FileTypes: []string{jsonFileType, yamlFileType, octetStream}, Summary: "Get a connector's OpenAPI specification",
				Text: "Returns the stored OpenAPI specification as a file, not wrapped in a data envelope. If the connector has none, the request fails with 404.", Tag: connectorsTag},
			Handler: Handle(h.downloadResource, "Unable to download connector resource")},
		{Method: http.MethodPut, Path: connectorPath + "/resource", Scope: rbac.ScopeConnectorUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "uploadConnectorResource", Summary: "Replace a connector's OpenAPI specification",
				Text: "Uploads an OpenAPI specification of up to 10 MiB, sent as multipart form data in a part named file, and points the connector's specification_url at it. Only api/openapi connectors accept one. Any other form part fails with 400.", Tag: connectorsTag,
				Errors: []int{http.StatusUnprocessableEntity}},
			Handler: Handle(h.uploadResource, "Unable to upload connector resource")},
		{Method: http.MethodGet, Path: connectorPath + "/specification", Scope: rbac.ScopeConnectorRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "readConnectorSpecification", Summary: "Get an OpenAPI connector's external specification",
				Text: "Fetches the OpenAPI document at the connector's stored specification_url and returns it with its format, detected from the content. The platform fetches it, so this works for a document the browser cannot fetch itself. If the connector has an uploaded specification or no external URL, the request fails with 404. If the host cannot be read or does not serve an OpenAPI document, the request fails with 502.", Tag: connectorsTag,
				Errors: []int{http.StatusBadGateway}},
			Handler: Handle(h.readSpecification, "Unable to read the OpenAPI specification")},
	}
}

type ConnectorSpecification struct {
	Content string `json:"content" example:"{\"openapi\":\"3.0.0\",\"info\":{\"title\":\"Orders\",\"version\":\"1.0.0\"},\"paths\":{}}"`
	Format  string `json:"format" example:"json"`
}

func (ConnectorSpecification) ResponseHeaders() map[string]string {
	return map[string]string{"Cache-Control": "no-store"}
}

func (h *connectorHandler) readSpecification(ctx context.Context, req IDRequest) (ConnectorSpecification, error) {
	specURL, err := h.svc.ExternalSpecificationURL(ctx, req.ID)
	if err != nil {
		return ConnectorSpecification{}, err
	}
	spec, err := h.specReader.Read(ctx, specURL)
	if err != nil {
		return ConnectorSpecification{}, err
	}
	return ConnectorSpecification{Content: spec.Content, Format: spec.Format}, nil
}

type uploadResourceRequest struct {
	ID   string       `path:"id" json:"-"`
	Spec UploadedFile `form:"file" json:"-"`
}

func (h *connectorHandler) downloadResource(ctx context.Context, req IDRequest) (FileResponse, error) {
	rc, key, err := h.svc.DownloadResource(ctx, req.ID)
	if err != nil {
		return FileResponse{}, err
	}
	return FileResponse{ContentType: contentTypeForKey(key), Filename: path.Base(key), Body: rc}, nil
}

const (
	jsonFileType = "application/json"
	yamlFileType = "application/yaml"
)

func contentTypeForKey(key string) string {
	switch {
	case strings.HasSuffix(key, ".json"):
		return jsonFileType
	case strings.HasSuffix(key, ".yaml"), strings.HasSuffix(key, ".yml"):
		return yamlFileType
	default:
		return octetStream
	}
}

func (h *connectorHandler) uploadResource(ctx context.Context, req uploadResourceRequest) (Connector, error) {
	conn, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return Connector{}, err
	}
	if conn.Type != "api" || (conn.Subtype != "" && conn.Subtype != "openapi") {
		return Connector{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed,
			Details: map[string][]string{"connectorType": {"a specification can only be uploaded to an api/openapi connector"}}}
	}
	resp, err := h.svc.UploadResource(ctx, req.ID, req.Spec.Name, bytes.NewReader(req.Spec.Data))
	if err != nil {
		return Connector{}, err
	}
	return toConnector(resp), nil
}
