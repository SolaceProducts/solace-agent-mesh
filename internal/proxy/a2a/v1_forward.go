// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	a2av1 "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

func (a *A2AAdapter) forwardV1(ctx context.Context, task *proxy.TaskContext, req *a2a.Request, n *negotiation) <-chan proxy.ResponseEvent {
	ch := make(chan proxy.ResponseEvent, 16)
	streaming := n.streaming && task.IsStreaming
	go func() {
		defer close(ch)
		first := n.chosen()
		relayed, err := a.forwardTo(ctx, task, req, first, streaming, ch)
		if next, ok := n.otherVersionThan(first.Version); ok && err != nil && !relayed && errors.Is(err, a2av1.ErrVersionNotSupported) {
			a.logger.Warn("remote agent rejected the A2A version, trying an interface of another version",
				"taskID", task.TaskID, "traceID", task.TraceID,
				"rejectedVersion", first.Version, "rejectedBinding", first.Binding,
				"protocolVersion", next.Version, "protocolBinding", next.Binding)
			_, err = a.forwardTo(ctx, task, req, next, streaming, ch)
		}
		if err != nil {
			relay(ctx, ch, proxy.ResponseEvent{Error: err})
		}
	}()
	return ch
}

func (a *A2AAdapter) forwardTo(ctx context.Context, task *proxy.TaskContext, req *a2a.Request, ri proxy.RemoteInterface, streaming bool, ch chan<- proxy.ResponseEvent) (bool, error) {
	task.SetRemoteInterface(&ri)
	if ri.Version == proxy.ProtocolVersion10 {
		return a.sendV1(ctx, task, req, ri, streaming, ch)
	}
	events, err := a.forwardV03(ctx, task, req, a.targetURL(ri))
	if err != nil {
		return false, err
	}
	relayed := false
	for evt := range events {
		if !relay(ctx, ch, evt) {
			return relayed, ctx.Err()
		}
		relayed = true
	}
	return relayed, nil
}

func (a *A2AAdapter) sendV1(ctx context.Context, task *proxy.TaskContext, req *a2a.Request, ri proxy.RemoteInterface, streaming bool, ch chan<- proxy.ResponseEvent) (bool, error) {
	transport, err := a.v1Transport(ri)
	if err != nil {
		return false, err
	}
	defer func() { _ = transport.Destroy() }()

	sendReq, err := toV1SendMessageRequest(req.Params, ri.Tenant)
	if err != nil {
		return false, err
	}
	params := v1ServiceParams(task)

	if !streaming {
		result, err := transport.SendMessage(ctx, params, sendReq)
		if err != nil {
			return false, a.remoteError(task, ri.Binding, err)
		}
		events, ends := fromV1Event(result)
		if !ends {
			state := a2av1.TaskStateUnspecified
			if t, ok := result.(*a2av1.Task); ok {
				state = t.Status.State
			}
			return false, fmt.Errorf("%w (%s)", errV1TaskUnfinished, state)
		}
		return relayAll(ctx, ch, events)
	}

	relayed := false
	for ev, err := range transport.SendStreamingMessage(ctx, params, sendReq) {
		if err != nil {
			return relayed, a.remoteError(task, ri.Binding, err)
		}
		events, ends := fromV1Event(ev)
		sent, err := relayAll(ctx, ch, events)
		relayed = relayed || sent
		if err != nil || ends {
			return relayed, err
		}
	}
	return relayed, nil
}

var errUnsafeRemoteTaskID = errors.New("remote agent task id cannot be used in an HTTP+JSON request path; the remote task may still be running")

func (a *A2AAdapter) cancelV1(ctx context.Context, task *proxy.TaskContext, ri proxy.RemoteInterface) error {
	if ri.Binding == proxy.RemoteBindingHTTPJSON && !isPathSegment(task.DownstreamTaskID) {
		return errUnsafeRemoteTaskID
	}
	transport, err := a.v1Transport(ri)
	if err != nil {
		return err
	}
	defer func() { _ = transport.Destroy() }()
	_, err = transport.CancelTask(ctx, v1ServiceParams(task), &a2av1.CancelTaskRequest{
		Tenant: ri.Tenant,
		ID:     a2av1.TaskID(task.DownstreamTaskID),
	})
	if err != nil {
		return a.remoteError(task, ri.Binding, err)
	}
	return nil
}

var (
	errInvalidV1Response  = errors.New("remote agent returned an invalid A2A 1.0 response")
	errV1StreamClosed     = errors.New("remote agent closed the connection before the task finished")
	errV1ResponseTooLarge = fmt.Errorf("remote agent response exceeded %d MiB", maxV1ResponseBytes>>20)
	errV1TaskAuth         = errors.New("could not authorize the request to the remote agent")
	errV1TLS              = errors.New("remote agent TLS certificate could not be verified")
	errV1TaskUnfinished   = errors.New("remote agent answered before its task finished")
)

const maxRemoteErrorMessage = 256

const (
	sdkJSONRPCStatusPrefix = "unexpected HTTP status: "
	sdkRESTStatusPrefix    = "unexpected HTTP status "
)

type v1CallError struct {
	text  string
	cause error
}

func (e *v1CallError) Error() string { return e.text }
func (e *v1CallError) Unwrap() error { return e.cause }

type v1StatusError struct {
	v1CallError
	status int
}

func (e *v1StatusError) StatusCode() int { return e.status }

type v1RemoteA2AError struct {
	v1CallError
	code int
}

func (e *v1RemoteA2AError) JSONRPCErrorCode() int { return e.code }

var a2aErrorCodes = []struct {
	err  error
	code int
}{
	{a2av1.ErrParseError, -32700},
	{a2av1.ErrInvalidRequest, -32600},
	{a2av1.ErrMethodNotFound, -32601},
	{a2av1.ErrInvalidParams, -32602},
	{a2av1.ErrInternalError, -32603},
	{a2av1.ErrServerError, -32000},
	{a2av1.ErrTaskNotFound, -32001},
	{a2av1.ErrTaskNotCancelable, -32002},
	{a2av1.ErrPushNotificationNotSupported, -32003},
	{a2av1.ErrUnsupportedOperation, -32004},
	{a2av1.ErrUnsupportedContentType, -32005},
	{a2av1.ErrInvalidAgentResponse, -32006},
	{a2av1.ErrExtendedCardNotConfigured, -32007},
	{a2av1.ErrExtensionSupportRequired, -32008},
	{a2av1.ErrVersionNotSupported, -32009},
	{a2av1.ErrUnauthenticated, -31401},
	{a2av1.ErrUnauthorized, -31403},
}

func a2aErrorCode(err error) int {
	for _, c := range a2aErrorCodes {
		if errors.Is(err, c.err) {
			return c.code
		}
	}
	return -32603
}

func (a *A2AAdapter) remoteError(task *proxy.TaskContext, binding string, err error) error {
	safe := safeV1Error(err, binding)
	attrs := []any{"taskID", task.TaskID, "traceID", task.TraceID, "error", safe}
	if _, ok := errors.AsType[*url.Error](err); ok {
		attrs = append(attrs, "cause", httputil.TransportErrorCause(err))
	} else {
		attrs = append(attrs, "causeType", fmt.Sprintf("%T", innermostError(err)))
	}
	a.logger.Debug("remote agent A2A 1.0 call failed", attrs...)
	return safe
}

func safeV1Error(err error, binding string) error {
	if a2aErr, ok := errors.AsType[*a2av1.Error](err); ok {
		if binding == proxy.RemoteBindingHTTPJSON && isRESTStatusError(a2aErr) {
			if status, ok := httpStatusAfter(a2aErr.Message, sdkRESTStatusPrefix); ok {
				return newV1StatusError(status, err)
			}
		}
		text := fmt.Sprintf("%v: %s", a2aErr.Err, truncateRunes(a2aErr.Message, maxRemoteErrorMessage))
		return &v1RemoteA2AError{v1CallError: v1CallError{text: text, cause: err}, code: a2aErrorCode(a2aErr.Err)}
	}
	if status, ok := httpStatusAfter(err.Error(), sdkJSONRPCStatusPrefix); ok && binding != proxy.RemoteBindingHTTPJSON {
		return newV1StatusError(status, err)
	}
	return &v1CallError{text: v1FailureText(err), cause: err}
}

func newV1StatusError(status int, cause error) *v1StatusError {
	text := strings.TrimSpace(fmt.Sprintf("unexpected HTTP status: %d %s", status, http.StatusText(status)))
	return &v1StatusError{v1CallError: v1CallError{text: text, cause: cause}, status: status}
}

func v1FailureText(err error) string {
	for _, sentinel := range []error{errCrossOriginRedirect, errTooManyV1Redirects, errV1TaskAuth} {
		if errors.Is(err, sentinel) {
			return sentinel.Error()
		}
	}
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return errV1ResponseTooLarge.Error()
	}
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return errV1TLS.Error()
	}
	if _, ok := errors.AsType[*url.Error](err); ok || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return httputil.SafeTransportErrorDetail(err)
	}
	if _, ok := errors.AsType[*net.OpError](err); ok || errors.Is(err, io.ErrUnexpectedEOF) {
		return errV1StreamClosed.Error()
	}
	return errInvalidV1Response.Error()
}

func isRESTStatusError(a2aErr *a2av1.Error) bool {
	switch a2aErr.Err {
	case a2av1.ErrUnauthenticated, a2av1.ErrUnauthorized, a2av1.ErrMethodNotFound, a2av1.ErrServerError:
		return true
	default:
		return false
	}
}

func httpStatusAfter(s, prefix string) (int, bool) {
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok || len(rest) < 3 {
		return 0, false
	}
	status, err := strconv.Atoi(rest[:3])
	return status, err == nil && status >= 100
}

func innermostError(err error) error {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err
		}
		err = next
	}
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

func (a *A2AAdapter) v1Transport(ri proxy.RemoteInterface) (a2aclient.Transport, error) {
	target := a.targetURL(ri)
	if ri.Binding != proxy.RemoteBindingHTTPJSON {
		return a2aclient.NewJSONRPCTransport(target, a.v1Client), nil
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil, errors.New("remote agent HTTP+JSON interface URL is invalid")
	}
	return a2aclient.NewRESTTransport(u, a.v1RESTClient), nil
}

func v1ServiceParams(task *proxy.TaskContext) a2aclient.ServiceParams {
	params := a2aclient.ServiceParams{a2av1.SvcParamVersion: {string(a2av1.Version)}}
	if task.OAuth2Token != "" {
		params["Authorization"] = []string{"Bearer " + task.OAuth2Token}
	}
	return params
}

func relayAll(ctx context.Context, ch chan<- proxy.ResponseEvent, events []proxy.ResponseEvent) (bool, error) {
	relayed := false
	for _, evt := range events {
		if !relay(ctx, ch, evt) {
			return relayed, ctx.Err()
		}
		relayed = true
	}
	return relayed, nil
}

func relay(ctx context.Context, ch chan<- proxy.ResponseEvent, evt proxy.ResponseEvent) bool {
	select {
	case ch <- evt:
		return true
	default:
	}
	select {
	case ch <- evt:
		return true
	case <-ctx.Done():
		return false
	}
}

type taskAuthTransport struct {
	base             http.RoundTripper
	headers          []proxy.HeaderConfig
	auth             *authProvider
	streamsAlwaysSSE bool
}

func (t *taskAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.authorizedRoundTrip(req)
	if err != nil {
		return nil, err
	}
	if !t.readAsStream(req, resp) {
		resp.Body = http.MaxBytesReader(nil, resp.Body, maxV1ResponseBytes)
	}
	return resp, nil
}

func (t *taskAuthTransport) readAsStream(req *http.Request, resp *http.Response) bool {
	return asksForStream(req) && (t.streamsAlwaysSSE || answersWithStream(resp))
}

func userBearerToken(req *http.Request) string {
	if auth := req.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return auth
	}
	return ""
}

func (t *taskAuthTransport) authorizedRoundTrip(req *http.Request) (*http.Response, error) {
	userToken := userBearerToken(req)
	out, err := t.authorize(req, userToken)
	if err != nil {
		closeBody(req)
		return nil, err
	}
	resp, err := t.base.RoundTrip(out)
	if err != nil || resp.StatusCode != http.StatusUnauthorized || userToken != "" || !t.auth.refreshesToken() || req.GetBody == nil {
		return resp, err
	}

	_, _ = io.CopyN(io.Discard, resp.Body, maxDrainBytes)
	_ = resp.Body.Close()
	t.auth.invalidateToken()

	retry, err := t.authorize(req, "")
	if err != nil {
		return nil, err
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("rewind request body: %w", err)
	}
	retry.Body = body
	return t.base.RoundTrip(retry)
}

func (t *taskAuthTransport) authorize(req *http.Request, userToken string) (*http.Request, error) {
	out := req.Clone(req.Context())
	applyHeaders(out, t.headers)
	if userToken != "" {
		out.Header.Set("Authorization", userToken)
		return out, nil
	}
	if err := t.auth.applyAuth(out.Context(), out); err != nil {
		return nil, fmt.Errorf("%w: %w", errV1TaskAuth, err)
	}
	return out, nil
}

const maxV1ResponseBytes = 10 << 20

const maxDrainBytes = 4 << 10

func asksForStream(req *http.Request) bool {
	return strings.Contains(req.Header.Get("Accept"), "text/event-stream")
}

func answersWithStream(resp *http.Response) bool {
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	return err != nil || mediaType == "text/event-stream"
}

const maxV1Redirects = 10

var (
	errCrossOriginRedirect = errors.New("remote agent redirected to another origin; not following with task credentials")
	errTooManyV1Redirects  = fmt.Errorf("remote agent redirected more than %d times", maxV1Redirects)
)

func refuseCrossOriginRedirect(req *http.Request, via []*http.Request) error {
	first := via[0].URL
	if req.URL.Scheme != first.Scheme || req.URL.Host != first.Host {
		return errCrossOriginRedirect
	}
	if len(via) >= maxV1Redirects {
		return errTooManyV1Redirects
	}
	return nil
}

func closeBody(req *http.Request) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
}
