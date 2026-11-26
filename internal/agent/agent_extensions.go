// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func (a *Agent) RegisterExtensionMethod(uri, method string, h extensions.Handler) {
	a.extensionMethodsMu.Lock()
	a.extensionMethods[method] = uri
	a.extensionMethodsMu.Unlock()
	a.extensions.Register(uri, method, h)
}

func (a *Agent) extensionMethodURI(method string) (string, bool) {
	a.extensionMethodsMu.RLock()
	defer a.extensionMethodsMu.RUnlock()
	uri, ok := a.extensionMethods[method]
	return uri, ok
}

func (a *Agent) hasExtensionMethod(method string) bool {
	_, ok := a.extensionMethodURI(method)
	return ok
}

func (a *Agent) handleExtensionRequest(ctx context.Context, msg *broker.Message, req *a2a.Request) {
	props := a2a.DecodeUserProperties(msg.UserProperties)
	replyTo := props.ReplyTo
	if replyTo == "" {
		a.logger.Warn("extension request missing replyTo; dropping",
			"method", req.Method,
			"id", req.ID,
		)
		return
	}

	uri, ok := a.extensionMethodURI(req.Method)
	if !ok {
		a.publishExtensionError(ctx, replyTo, req.ID,
			extensions.CodeMethodNotFound,
			fmt.Sprintf("method %q is not a registered extension method", req.Method))
		return
	}

	if !activationListIncludes(props.A2AExtensions, uri) {
		a.publishExtensionError(ctx, replyTo, req.ID,
			extensions.CodeMethodNotFound,
			fmt.Sprintf("extension %q not activated for this request", uri))
		return
	}

	var rawParams json.RawMessage
	if req.Params != nil {
		buf, err := json.Marshal(req.Params)
		if err != nil {
			a.publishExtensionError(ctx, replyTo, req.ID,
				extensions.CodeInternal,
				"failed to re-marshal params: "+err.Error())
			return
		}
		rawParams = buf
	}

	info := extensions.RequestInfo{UserID: props.UserID, ClientID: props.ClientID}
	result, dispatchErr := a.extensions.Dispatch(ctx, uri, req.Method, info, rawParams)
	if dispatchErr != nil {
		a.publishExtensionResult(ctx, replyTo, req.ID, nil, dispatchErr)
		return
	}
	a.publishExtensionResult(ctx, replyTo, req.ID, result, nil)
}

func (a *Agent) publishExtensionError(ctx context.Context, replyTo, id string, code int, message string) {
	a.publishExtensionResult(ctx, replyTo, id, nil, &extensions.Error{Code: code, Message: message})
}

func (a *Agent) publishExtensionResult(ctx context.Context, replyTo, id string, result any, errObj *extensions.Error) {
	resp := &a2a.Response{
		JSONRPC: "2.0",
		ID:      id,
	}
	if errObj != nil {
		resp.Error = &a2a.Error{Code: errObj.Code, Message: errObj.Message, Data: errObj.Data}
	} else {
		resp.Result = result
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		a.logger.Error("failed to marshal extension response", "error", err, "id", id)
		return
	}
	if pubErr := a.svc.Broker().Publish(ctx, replyTo, &broker.Message{Payload: payload}); pubErr != nil {
		a.logger.Error("failed to publish extension response",
			"error", pubErr,
			"replyTo", replyTo,
			"id", id,
		)
	}
}

func activationListIncludes(header, uri string) bool {
	if header == "" {
		return false
	}
	for entry := range strings.SplitSeq(header, ",") {
		if strings.TrimSpace(entry) == uri {
			return true
		}
	}
	return false
}
