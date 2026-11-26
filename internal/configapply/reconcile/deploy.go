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
	"net/http"
	"strings"
	"time"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

const toolsetNotReadyPollInterval = 500 * time.Millisecond

type Deployer interface {
	DeployResource(ctx context.Context, id string, action string) error
}

type deployResult struct {
	Kind   string
	Name   string
	ID     string
	Action string
	Err    error
}

func updateActionFor(status string) string {
	if status == "" || status == entplatform.DisplayStatusNotDeployed {
		return "deploy"
	}
	return "update"
}

type workflowDeployer struct {
	c *Client
}

func newWorkflowDeployer(c *Client) *workflowDeployer {
	return &workflowDeployer{c: c}
}

func (d *workflowDeployer) DeployResource(ctx context.Context, id string, action string) error {
	return d.c.deploy(ctx, workflowDeploymentsResource, apiv2.CreateWorkflowDeploymentRequest{WorkflowID: id, Action: action})
}

var (
	agentDeploymentsResource      = platformResource{path: "/agentDeployments"}
	workflowDeploymentsResource   = platformResource{path: "/workflowDeployments"}
	entrypointDeploymentsResource = platformResource{path: "/entrypointDeployments"}
)

func (c *Client) deploy(ctx context.Context, r platformResource, req any) error {
	return c.doDeploy(ctx, r.apiPath(), req, nil)
}

func runWorkflowDeployments(ctx context.Context, d Deployer, progress io.Writer, outcome *workflowKindOutcome) {
	if outcome == nil || d == nil {
		return
	}
	results, failures := deployKind(ctx, d, progress, "workflow", outcome.Created, outcome.Updated, outcome.OutOfSync)
	outcome.Deployments = append(outcome.Deployments, results...)
	outcome.Failures = append(outcome.Failures, failures...)
}

type agentDeployer struct {
	c *Client
}

func newAgentDeployer(c *Client) *agentDeployer {
	return &agentDeployer{c: c}
}

func (d *agentDeployer) DeployResource(ctx context.Context, id string, action string) error {
	req := apiv2.CreateAgentDeploymentRequest{AgentID: id, Action: action}
	deadline := time.Now().Add(toolsetDiscoveryTimeout)
	for {
		err := d.c.deploy(ctx, agentDeploymentsResource, req)
		if err == nil {
			return nil
		}
		if !isToolsetNotReady(err) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(toolsetNotReadyPollInterval):
		}
	}
}

func isToolsetNotReady(err error) bool {
	he, ok := errors.AsType[*HTTPError](err)
	if !ok {
		return false
	}
	return he.StatusCode == http.StatusUnprocessableEntity &&
		(strings.Contains(he.Body, "is not ready") || strings.Contains(he.Body, "wait for STR"))
}

type gatewayDeployer struct {
	c *Client
}

func newGatewayDeployer(c *Client) *gatewayDeployer {
	return &gatewayDeployer{c: c}
}

func (d *gatewayDeployer) DeployResource(ctx context.Context, id string, action string) error {
	return d.c.deploy(ctx, entrypointDeploymentsResource, apiv2.CreateEntrypointDeploymentRequest{EntrypointID: id, Action: action})
}

const deployingLine = "deploying %s/%s (%s)...\n"

type deployRef struct {
	name, id, status string
}

type deployable[R any] interface {
	deployRef() deployRef
	deployFailure(err error) R
}

func (r agentOpResult) deployRef() deployRef {
	return deployRef{name: r.Op.Name, id: r.Op.ID, status: r.DeploymentStatus}
}

func (r agentOpResult) deployFailure(err error) agentOpResult {
	return agentOpResult{Op: r.Op, Verb: "deploy", Err: err}
}

func (r gatewayOpResult) deployRef() deployRef {
	return deployRef{name: r.Op.Name, id: r.Op.ID, status: r.DeploymentStatus}
}

func (r gatewayOpResult) deployFailure(err error) gatewayOpResult {
	return gatewayOpResult{Op: r.Op, Verb: "deploy", Err: err}
}

func (r workflowOpResult) deployRef() deployRef {
	return deployRef{name: r.Op.Name, id: r.Op.ID, status: r.DeploymentStatus}
}

func (r workflowOpResult) deployFailure(err error) workflowOpResult {
	return workflowOpResult{Op: r.Op, Verb: "deploy", Err: err}
}

func deployKind[R deployable[R]](ctx context.Context, d Deployer, progress io.Writer, kind string,
	created, updated, outOfSync []R) (results []deployResult, failures []R) {
	deployOne := func(r R, action, missingID string) {
		rf := r.deployRef()
		dr := deployResult{Kind: kind, Name: rf.name, ID: rf.id, Action: action}
		if rf.id == "" {
			dr.Err = fmt.Errorf("%s %q: %s", kind, rf.name, missingID)
		} else {
			_, _ = fmt.Fprintf(progress, deployingLine, kind, rf.name, action)
			dr.Err = d.DeployResource(ctx, rf.id, action)
		}
		results = append(results, dr)
		if dr.Err != nil {
			failures = append(failures, r.deployFailure(dr.Err))
		}
	}
	for _, r := range created {
		deployOne(r, "deploy", "missing platform ID after create")
	}
	for _, r := range updated {
		deployOne(r, updateActionFor(r.deployRef().status), "missing platform ID after update")
	}
	for _, r := range outOfSync {
		deployOne(r, updateActionFor(r.deployRef().status), "missing platform ID for out-of-sync re-deploy")
	}
	return results, failures
}

func runGatewayDeployments(ctx context.Context, d Deployer, progress io.Writer, outcome *gatewayKindOutcome) {
	if outcome == nil || d == nil {
		return
	}
	results, failures := deployKind(ctx, d, progress, "entrypoint", outcome.Created, outcome.Updated, outcome.OutOfSync)
	outcome.Deployments = append(outcome.Deployments, results...)
	outcome.Failures = append(outcome.Failures, failures...)
}

func runAgentDeployments(ctx context.Context, d Deployer, progress io.Writer, outcome *agentKindOutcome) {
	if outcome == nil || d == nil {
		return
	}
	results, failures := deployKind(ctx, d, progress, "agent", outcome.Created, outcome.Updated, outcome.OutOfSync)
	outcome.Deployments = append(outcome.Deployments, results...)
	outcome.Failures = append(outcome.Failures, failures...)
}
