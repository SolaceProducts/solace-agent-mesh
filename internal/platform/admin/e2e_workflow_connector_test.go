// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
)

var (
	sendEmailToolPath string
	sendEmailToolOnce sync.Once
)

func getSendEmailTool(t *testing.T) string {
	t.Helper()
	sendEmailToolOnce.Do(func() {
		pkgDir, err := os.Getwd()
		if err != nil {
			t.Fatalf("get the package directory: %v", err)
		}
		srcDir := filepath.Join(pkgDir, "..", "..", "..", "tools", "go", "send_email")

		tmpDir, err := os.MkdirTemp("", "e2e-send-email-*") //nolint:usetesting // sync.Once cache shared by every test; t.TempDir would delete it after the first
		if err != nil {
			t.Fatalf("create temp dir for send_email: %v", err)
		}
		binPath := filepath.Join(tmpDir, "send_email")

		cmd := exec.Command("go", "build", "-o", binPath, ".")
		cmd.Dir = srcDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			_ = os.RemoveAll(tmpDir)
			t.Fatalf("build send_email tool: %v\n%s", err, out)
		}
		sendEmailToolPath = binPath
	})
	if sendEmailToolPath == "" {
		t.Fatal("send_email binary not built")
	}
	return sendEmailToolPath
}

type receivedEmail struct {
	From string
	To   []string
	Data string
}

func startTestSMTPServer(t *testing.T) (string, <-chan receivedEmail) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	received := make(chan receivedEmail, 4)
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go serveSMTPConn(conn, received)
		}
	}()
	return ln.Addr().String(), received
}

func serveSMTPConn(conn net.Conn, received chan<- receivedEmail) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	write := func(s string) bool {
		_, err := conn.Write([]byte(s + "\r\n"))
		return err == nil
	}
	if !write("220 test-smtp ready") {
		return
	}
	var mail receivedEmail
	var data strings.Builder
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				mail.Data = data.String()
				received <- mail
				mail = receivedEmail{}
				data.Reset()
				inData = false
				if !write("250 accepted") {
					return
				}
				continue
			}
			data.WriteString(line)
			data.WriteString("\n")
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			if !write("250 test-smtp") {
				return
			}
		case strings.HasPrefix(upper, "MAIL FROM:"):
			mail.From = strings.Trim(line[len("MAIL FROM:"):], "<> ")
			if !write("250 ok") {
				return
			}
		case strings.HasPrefix(upper, "RCPT TO:"):
			mail.To = append(mail.To, strings.Trim(line[len("RCPT TO:"):], "<> "))
			if !write("250 ok") {
				return
			}
		case strings.HasPrefix(upper, "DATA"):
			inData = true
			if !write("354 go ahead") {
				return
			}
		case strings.HasPrefix(upper, "QUIT"):
			_ = write("221 bye")
			return
		default:
			if !write("250 ok") {
				return
			}
		}
	}
}

func emailConnectorValuesFor(t *testing.T, smtpAddr string) string {
	t.Helper()
	host, port, err := net.SplitHostPort(smtpAddr)
	if err != nil {
		t.Fatalf("split smtp addr: %v", err)
	}
	return fmt.Sprintf(`{"smtp_host":%q,"smtp_port":%q,"smtp_tls":"none","smtp_auth_type":"none","envelope_from_none":"workflow@example.com","recipient_allowlist":"example.com"}`, host, port)
}

func TestE2E_WorkflowConnectorRefExecution(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, nil)

	toolBin := getSendEmailTool(t)
	manifestPath := generateSTRManifest(t, toolBin)
	workerCfg := str.WorkerConfig{
		WorkerID:      "e2e-str-email",
		Namespace:     h.a2aNamespace,
		ManifestPaths: []string{manifestPath},
		Sandbox: str.SandboxConfig{
			Mode:                    "direct",
			MaxConcurrentExecutions: 4,
		},
	}
	worker := str.NewWorker(h.rt.Services(), workerCfg)
	if err := worker.Init(h.ctx, nil); err != nil {
		t.Fatalf("str worker init: %v", err)
	}
	if err := worker.Start(h.ctx); err != nil {
		t.Fatalf("str worker start: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })

	smtpAddr, received := startTestSMTPServer(t)
	createHydrationConnector(t, h.connectorRepo, "e2e-mailer", "email", "smtp", emailConnectorValuesFor(t, smtpAddr))

	appConfig := map[string]any{
		"output_mapping": map[string]any{"result": "{{nodes.send.output}}"},
		"nodes": []any{
			map[string]any{
				"id":        "send",
				"type":      "tool",
				"connector": "e2e-mailer",
				"input": map[string]any{
					"to":      []any{"dest@example.com"},
					"subject": "connector ref e2e",
					"body":    "delivered via connector hydration",
				},
			},
		},
	}
	wfID := h.createWorkflow("e2e_connector_ref_workflow", appConfig)
	h.deployWorkflow(wfID)
	wfInstanceName := workflowInstanceName(wfID)
	h.waitForInstance(wfInstanceName, 5*time.Second)
	time.Sleep(500 * time.Millisecond)

	events := h.sendMessage(wfInstanceName, "send the email", 30*time.Second)
	h.assertHasResponseEvent(events)

	select {
	case mail := <-received:
		if len(mail.To) != 1 || mail.To[0] != "dest@example.com" {
			t.Errorf("mail recipients = %v, want [dest@example.com]", mail.To)
		}
		if mail.From != "workflow@example.com" {
			t.Errorf("mail from = %q, want workflow@example.com", mail.From)
		}
		if !strings.Contains(mail.Data, "delivered via connector hydration") {
			t.Errorf("mail body missing workflow content:\n%s", mail.Data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no email received by SMTP listener within 5s")
	}

	storedWf, err := h.workflowRepo.GetByID(h.ctx, wfID)
	if err != nil {
		t.Fatalf("get workflow: %v", err)
	}
	if !strings.Contains(storedWf.AppConfig, `"connector":"e2e-mailer"`) {
		t.Errorf("stored appConfig lost the symbolic node connector ref:\n%s", storedWf.AppConfig)
	}
	if strings.Contains(storedWf.AppConfig, "send_email_") {
		t.Error("stored appConfig was hydrated in place — must keep the symbolic form")
	}
}

func TestE2E_ConnectorUpdateRedeploysWorkflow(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, nil)

	createHydrationConnector(t, h.connectorRepo, "drift-mailer", "email", "smtp",
		`{"smtp_host":"old.example.com","smtp_port":"2525","smtp_tls":"none","smtp_auth_type":"none","envelope_from_none":"a@example.com","recipient_allowlist":"example.com"}`)

	appConfig := map[string]any{
		"output_mapping": map[string]any{"result": "{{nodes.send.output}}"},
		"nodes": []any{
			map[string]any{
				"id":        "send",
				"type":      "tool",
				"connector": "drift-mailer",
				"input":     map[string]any{"to": []any{"x@example.com"}, "subject": "s", "body": "b"},
			},
		},
	}
	wfID := h.createWorkflow("e2e_drift_workflow", appConfig)
	h.deployWorkflow(wfID)

	connRow, err := h.connectorRepo.GetAllByName(h.ctx, "drift-mailer")
	if err != nil || len(connRow) != 1 {
		t.Fatalf("get connector row: %v (%d rows)", err, len(connRow))
	}
	handler := &connectorRedeployer{
		workflowRepo: h.workflowRepo,
	}
	affected := handler.findWorkflowsByConnector(h.ctx, connRow[0].ID)
	if len(affected) != 1 || affected[0] != wfID {
		t.Fatalf("findWorkflowsByConnector = %v, want [%s]", affected, wfID)
	}

	newVals := `{"smtp_host":"new.example.com","smtp_port":"2525","smtp_tls":"none","smtp_auth_type":"none","envelope_from_none":"a@example.com","recipient_allowlist":"example.com"}`
	if updateErr := h.connectorRepo.UpdateValues(h.ctx, connRow[0].ID, newVals); updateErr != nil {
		t.Fatalf("update connector values: %v", updateErr)
	}

	pub := NewRedeployPublisher(h.deploySvc, h.agentRepo, testLogger()).
		WithWorkflowRedeploy(h.wfDeploySvc, h.workflowRepo)
	event := store.OutboxEvent{
		EventType:   "connector.updated",
		AggregateID: connRow[0].ID,
		Payload:     fmt.Sprintf(`{"agentIds":[],"workflowIds":[%q],"userId":"editor"}`, wfID),
	}
	if pubErr := pub.PublishOutboxEvent(h.ctx, event); pubErr != nil {
		t.Fatalf("publish outbox event: %v", pubErr)
	}

	deployments, _, err := h.wfDeployRepo.ListPaginated(h.ctx, 1, 10)
	if err != nil {
		t.Fatalf("list workflow deployments: %v", err)
	}
	var latest *store.WorkflowDeployment
	for i := range deployments {
		d := &deployments[i]
		if d.WorkflowID != wfID {
			continue
		}
		if latest == nil || d.CreatedTime > latest.CreatedTime {
			latest = d
		}
	}
	if latest == nil {
		t.Fatal("no deployment rows for workflow")
	}
	if latest.CreatedBy != "editor" {
		t.Fatalf("latest deployment CreatedBy = %q, want the outbox event's user \"editor\"", latest.CreatedBy)
	}
	if latest.Status != DeployStatusSuccess {
		t.Fatalf("redeploy status = %q, want %q (error: %v)", latest.Status, DeployStatusSuccess, latest.ErrorMessage)
	}
	if latest.WorkflowConfigSnapshot == nil || !strings.Contains(*latest.WorkflowConfigSnapshot, "new.example.com") {
		t.Error("redeployed snapshot not hydrated from the updated connector values")
	}
}

func TestE2E_APIConnectorUpdateRedeploysWorkflow(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, nil)

	specSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`openapi: 3.0.0
info:
  title: Petstore
  version: 1.0.0
servers:
  - url: https://old.example.com
paths:
  /pets:
    get:
      operationId: listPets
      responses:
        "200":
          description: ok
`))
	}))
	t.Cleanup(specSrv.Close)

	values := func(baseURL string) string {
		return fmt.Sprintf(`{"base_url":%q,"auth_type":"none","allow_list":"listPets","specification_url":%q}`,
			baseURL, specSrv.URL+"/petstore.yaml")
	}
	createHydrationConnector(t, h.connectorRepo, "drift-api", "api", "openapi", values("https://old.example.com"))

	appConfig := map[string]any{
		"output_mapping": map[string]any{"result": "{{nodes.fetch.output}}"},
		"nodes": []any{
			map[string]any{
				"id":        "fetch",
				"type":      "tool",
				"connector": "drift-api",
				"tool":      "list_pets",
				"input":     map[string]any{},
			},
		},
	}
	wfID := h.createWorkflow("e2e_api_drift_workflow", appConfig)
	deployed := h.deployWorkflow(wfID)
	if deployed.Status != DeployStatusSuccess {
		t.Fatalf("initial deploy status = %q (error: %s)", deployed.Status, deployed.ErrorMessage)
	}

	connRow, err := h.connectorRepo.GetAllByName(h.ctx, "drift-api")
	if err != nil || len(connRow) != 1 {
		t.Fatalf("get connector row: %v (%d rows)", err, len(connRow))
	}
	handler := &connectorRedeployer{workflowRepo: h.workflowRepo}
	if affected := handler.findWorkflowsByConnector(h.ctx, connRow[0].ID); len(affected) != 1 || affected[0] != wfID {
		t.Fatalf("findWorkflowsByConnector = %v, want [%s] (api binding must persist through workflow_connectors)", affected, wfID)
	}

	if updateErr := h.connectorRepo.UpdateValues(h.ctx, connRow[0].ID, values("https://new.example.com")); updateErr != nil {
		t.Fatalf("update connector values: %v", updateErr)
	}
	pub := NewRedeployPublisher(h.deploySvc, h.agentRepo, testLogger()).
		WithWorkflowRedeploy(h.wfDeploySvc, h.workflowRepo)
	event := store.OutboxEvent{
		EventType:   "connector.updated",
		AggregateID: connRow[0].ID,
		Payload:     fmt.Sprintf(`{"agentIds":[],"workflowIds":[%q],"userId":"editor"}`, wfID),
	}
	if pubErr := pub.PublishOutboxEvent(h.ctx, event); pubErr != nil {
		t.Fatalf("publish outbox event: %v", pubErr)
	}

	deployments, _, err := h.wfDeployRepo.ListPaginated(h.ctx, 1, 10)
	if err != nil {
		t.Fatalf("list workflow deployments: %v", err)
	}
	var latest *store.WorkflowDeployment
	for i := range deployments {
		d := &deployments[i]
		if d.WorkflowID != wfID {
			continue
		}
		if latest == nil || d.CreatedTime > latest.CreatedTime {
			latest = d
		}
	}
	if latest == nil {
		t.Fatal("no deployment rows for workflow")
	}
	if latest.CreatedBy != "editor" {
		t.Fatalf("latest deployment CreatedBy = %q, want the outbox event's user \"editor\"", latest.CreatedBy)
	}
	if latest.Status != DeployStatusSuccess {
		errMsg := ""
		if latest.ErrorMessage != nil {
			errMsg = *latest.ErrorMessage
		}
		t.Fatalf("redeploy status = %q, want %q (error: %s)", latest.Status, DeployStatusSuccess, errMsg)
	}
	if latest.WorkflowConfigSnapshot == nil {
		t.Fatal("redeploy stored no config snapshot")
	}
	snapshot := *latest.WorkflowConfigSnapshot
	if !strings.Contains(snapshot, "new.example.com") {
		t.Error("redeployed snapshot not hydrated from the updated connector values")
	}
	if !strings.Contains(snapshot, "tool_name: list_pets") {
		t.Errorf("redeployed snapshot missing the resolved node tool_name:\n%s", snapshot)
	}
	if strings.Contains(snapshot, "connector: drift-api") {
		t.Error("redeployed snapshot still carries the symbolic node connector field")
	}
}

func TestE2E_WorkflowDeployFailsOnDeletedConnector(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, nil)

	conn := createHydrationConnector(t, h.connectorRepo, "deleted-mailer", "email", "smtp", emailConnectorValues())

	appConfig := map[string]any{
		"output_mapping": map[string]any{"result": "{{nodes.send.output}}"},
		"nodes": []any{
			map[string]any{
				"id":        "send",
				"type":      "tool",
				"connector": "deleted-mailer",
				"input":     map[string]any{"to": []any{"x@example.com"}, "subject": "s", "body": "b"},
			},
		},
	}
	wfID := h.createWorkflow("e2e_missing_connector_workflow", appConfig)

	if delErr := h.connectorRepo.Delete(h.ctx, conn.ID); delErr != nil {
		t.Fatalf("delete connector: %v", delErr)
	}

	resp, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{
		WorkflowID: wfID, Action: DeployActionDeploy,
	}, "deployer")
	if err != nil {
		t.Fatalf("deployment Create returned transport error: %v", err)
	}
	if resp.Status != DeployStatusFailed {
		t.Fatalf("deploy status = %q, want %q", resp.Status, DeployStatusFailed)
	}
	for _, want := range []string{"resolve connector refs", "deleted-mailer", "not bound"} {
		if !strings.Contains(resp.ErrorMessage, want) {
			t.Errorf("deployment error %q missing %q", resp.ErrorMessage, want)
		}
	}

	if dbErr := h.wfDeployRepo.Create(h.ctx, &store.WorkflowDeployment{
		WorkflowID: wfID, Action: DeployActionDeploy, Status: DeployStatusSuccess, CreatedBy: "seed",
	}); dbErr != nil {
		t.Fatalf("seed success deployment: %v", dbErr)
	}
	pub := NewRedeployPublisher(h.deploySvc, h.agentRepo, testLogger()).
		WithWorkflowRedeploy(h.wfDeploySvc, h.workflowRepo)
	event := store.OutboxEvent{
		EventType:   "connector.deleted",
		AggregateID: "gone",
		Payload:     fmt.Sprintf(`{"agentIds":[],"workflowIds":[%q],"userId":"editor"}`, wfID),
	}
	if pubErr := pub.PublishOutboxEvent(h.ctx, event); pubErr != nil {
		t.Fatalf("workflow redeploy failure must not fail the outbox event, got %v", pubErr)
	}
}
