// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/clianalytics"
)

type Client struct {
	BaseURL string
	Token   string
	Timeout time.Duration
	Debug   bool
}

func (c *Client) FetchAgentCards() ([]AgentCard, error) {
	url := c.baseURL() + "/api/v1/agentCards"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching agent cards: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("agent cards: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var body struct {
		Data []AgentCard `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding agent cards: %w", err)
	}
	return body.Data, nil
}

func ResolveAgentName(cards []AgentCard, preferred string) string {
	prefLower := strings.ToLower(preferred)

	for _, c := range cards {
		if c.Name == preferred {
			return c.Name
		}
	}

	for _, c := range cards {
		if c.DisplayName() == preferred {
			return c.Name
		}
	}

	for _, c := range cards {
		if strings.EqualFold(c.Name, preferred) {
			return c.Name
		}
	}

	for _, c := range cards {
		if strings.EqualFold(c.DisplayName(), preferred) {
			return c.Name
		}
	}

	for _, c := range cards {
		nameLower := strings.ToLower(c.Name)
		dispLower := strings.ToLower(c.DisplayName())
		if strings.Contains(nameLower, prefLower) ||
			strings.Contains(prefLower, nameLower) ||
			strings.Contains(dispLower, prefLower) ||
			strings.Contains(prefLower, dispLower) {
			return c.Name
		}
	}

	return ""
}

func (c *Client) SendTask(agent, sessionID, message string, filePaths []string, data, siInputSchema, siOutputSchema string) (*TaskResponse, error) {
	url := c.baseURL() + "/api/v1/message:stream"

	parts := []MessagePart{
		{"kind": "text", "text": message},
	}

	for _, fp := range filePaths {
		filePart, err := buildFilePart(fp)
		if err != nil {
			return nil, fmt.Errorf("building file part %s: %w", fp, err)
		}
		parts = append(parts, filePart)
	}

	if siInputSchema != "" || siOutputSchema != "" {
		siPart, err := buildStructuredInvocationPart(siInputSchema, siOutputSchema)
		if err != nil {
			return nil, fmt.Errorf("building structured invocation part: %w", err)
		}
		parts = append(parts, siPart)
	}

	if data != "" {
		dataPart, err := buildDataPart(data)
		if err != nil {
			return nil, fmt.Errorf("building data part: %w", err)
		}
		parts = append(parts, dataPart)
	}

	payload := TaskRequest{
		JSONRPC: "2.0",
		ID:      idgen.New(),
		Method:  "message/stream",
		Params: TaskParams{
			Message: TaskMessage{
				Role:      "user",
				Parts:     parts,
				MessageID: idgen.New(),
				Kind:      "message",
				ContextID: sessionID,
				Metadata:  map[string]any{"agent_name": agent},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling task request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(req)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending task: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("send task: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var taskResp TaskResponse
	if err := json.NewDecoder(resp.Body).Decode(&taskResp); err != nil {
		return nil, fmt.Errorf("decoding task response: %w", err)
	}

	return &taskResp, nil
}

func (c *Client) DownloadArtifact(sessionID, filename, outputDir string) (*ArtifactInfo, error) {
	url := fmt.Sprintf("%s/api/v1/artifacts/%s/%s", c.baseURL(), sessionID, filename)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading artifact: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artifact download: HTTP %d", resp.StatusCode)
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading artifact body: %w", err)
	}

	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	safeName := filepath.Base(filename)
	localPath := filepath.Join(outputDir, "artifacts", safeName)
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating artifact directory: %w", err)
	}

	if err := os.WriteFile(localPath, content, 0o644); err != nil {
		return nil, fmt.Errorf("saving artifact: %w", err)
	}

	return &ArtifactInfo{
		Filename:  filename,
		Size:      len(content),
		MimeType:  mimeType,
		LocalPath: localPath,
	}, nil
}

func (c *Client) ListArtifacts(sessionID string) ([]map[string]any, error) {
	url := fmt.Sprintf("%s/api/v1/artifacts/%s", c.baseURL(), sessionID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing artifacts: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list artifacts: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading artifacts: %w", err)
	}
	return decodeArtifactList(body)
}

func decodeArtifactList(body []byte) ([]map[string]any, error) {
	var page struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &page); err == nil {
		return page.Data, nil
	}
	var artifacts []map[string]any
	if err := json.Unmarshal(body, &artifacts); err != nil {
		return nil, fmt.Errorf("decoding artifacts: %w", err)
	}
	return artifacts, nil
}

func (c *Client) FetchChatTasks(sessionID string) ([]byte, error) {
	url := fmt.Sprintf("%s/api/v1/sessions/%s/chatTasks", c.baseURL(), sessionID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching chat tasks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch chat tasks: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (c *Client) DownloadAllArtifacts(sessionID, outputDir string) ([]ArtifactInfo, error) {
	list, err := c.ListArtifacts(sessionID)
	if err != nil {
		return nil, err
	}

	var results []ArtifactInfo
	for _, a := range list {
		filename, _ := a["filename"].(string)
		if filename == "" {
			continue
		}
		info, err := c.DownloadArtifact(sessionID, filename, outputDir)
		if err != nil {
			continue
		}
		if info != nil {
			results = append(results, *info)
		}
	}
	return results, nil
}

func (c *Client) DownloadSTIM(taskID, outputDir string) error {
	url := fmt.Sprintf("%s/api/v1/tasks/%s", c.baseURL(), taskID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Accept", "application/x-yaml")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("downloading STIM: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("STIM download: HTTP %d", resp.StatusCode)
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	stimPath := filepath.Join(outputDir, taskID+".stim")
	return os.WriteFile(stimPath, content, 0o644)
}

func (c *Client) RespondToHIL(req HILRespondRequest) error {
	url := c.baseURL() + "/api/v1/userInput/respond"

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshaling HIL response: %w", err)
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.setHeaders(httpReq)

	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return fmt.Errorf("posting HIL response: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HIL respond: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (c *Client) baseURL() string {
	return strings.TrimRight(c.BaseURL, "/")
}

func (c *Client) setHeaders(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set(httputil.HeaderClientSurface, httputil.SurfaceCLI)
	httputil.SetAPIVersion(req)
	if session := clianalytics.SessionID(); session != "" {
		req.Header.Set(httputil.HeaderClientSession, session)
	}
}

func (c *Client) httpClient() *http.Client {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{Timeout: timeout}
}

func buildFilePart(path string) (MessagePart, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}

	mimeType := detectMIMEType(filepath.Base(abs), data)

	return MessagePart{
		"kind": "file",
		"file": map[string]any{
			"bytes":    base64.StdEncoding.EncodeToString(data),
			"name":     filepath.Base(abs),
			"mimeType": mimeType,
		},
	}, nil
}

func buildStructuredInvocationPart(inputSchemaPath, outputSchemaPath string) (MessagePart, error) {
	data := map[string]any{
		"type":          "structured_invocation_request",
		"workflow_name": "cli-test",
		"node_id":       fmt.Sprintf("cli-%s", idgen.New()[:8]),
	}

	if inputSchemaPath != "" {
		content, err := os.ReadFile(inputSchemaPath)
		if err != nil {
			return nil, fmt.Errorf("reading input schema file: %w", err)
		}
		var schema map[string]any
		if err := json.Unmarshal(content, &schema); err != nil {
			return nil, fmt.Errorf("parsing input schema JSON: %w", err)
		}
		data["input_schema"] = schema
	}

	if outputSchemaPath != "" {
		content, err := os.ReadFile(outputSchemaPath)
		if err != nil {
			return nil, fmt.Errorf("reading output schema file: %w", err)
		}
		var schema map[string]any
		if err := json.Unmarshal(content, &schema); err != nil {
			return nil, fmt.Errorf("parsing output schema JSON: %w", err)
		}
		data["output_schema"] = schema
	}

	return MessagePart{
		"kind": "data",
		"data": data,
	}, nil
}

func buildDataPart(dataStr string) (MessagePart, error) {
	var parsed any

	if strings.HasPrefix(dataStr, "@") {
		content, err := os.ReadFile(dataStr[1:])
		if err != nil {
			return nil, fmt.Errorf("reading data file: %w", err)
		}
		if err := json.Unmarshal(content, &parsed); err != nil {
			return nil, fmt.Errorf("parsing data file JSON: %w", err)
		}
	} else {
		if err := json.Unmarshal([]byte(dataStr), &parsed); err != nil {
			return nil, fmt.Errorf("parsing inline JSON data: %w", err)
		}
	}

	return MessagePart{
		"kind": "data",
		"data": parsed,
	}, nil
}

func detectMIMEType(filename string, content []byte) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".md", ".markdown":
		return "text/markdown"
	case ".txt":
		return "text/plain"
	case ".csv":
		return "text/csv"
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".xml":
		return "application/xml"
	case ".html", ".htm":
		return "text/html"
	case ".py":
		return "text/x-python"
	case ".go":
		return "text/x-go"
	case ".js":
		return "text/javascript"
	case ".ts":
		return "text/typescript"
	case ".css":
		return "text/css"
	case ".sh":
		return "text/x-shellscript"
	case ".sql":
		return "text/x-sql"
	case ".toml":
		return "application/toml"
	case ".ini", ".cfg":
		return "text/plain"
	case ".log":
		return "text/plain"
	}
	if mt := mime.TypeByExtension(ext); mt != "" {
		return mt
	}
	if len(content) > 0 {
		return http.DetectContentType(content)
	}
	return "application/octet-stream"
}
