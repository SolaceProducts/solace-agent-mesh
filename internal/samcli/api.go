// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/itchyny/gojq"
	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/clianalytics"
)

const (
	schemeHTTP  = "http://"
	schemeHTTPS = "https://"
)

type apiCall struct {
	method  string
	url     string
	token   string
	headers http.Header
	body    []byte
}

type apiOpts struct {
	method      string
	headers     []string
	fields      []string
	rawFields   []string
	input       string
	jqFilter    string
	raw         bool
	include     bool
	paginate    bool
	target      string
	url         string
	manifest    string
	insecure    bool
	verbose     bool
	positional  []string
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	httpClient  *http.Client
	resolveBase func(ctx context.Context, o *apiOpts) (string, error)
	resolveTok  func(ctx context.Context, baseURL, explicit string) string
}

func newAPICmd() *cobra.Command {
	o := &apiOpts{}
	cmd := &cobra.Command{
		Use:   "api [METHOD] PATH",
		Short: "Make direct, authenticated API calls to Agent Mesh",
		Long: `Make an authenticated HTTP request to the Agent Mesh API.

sam api is intended for interactive and scripted operator use only. Do not build external system integrations
on top of it; the endpoints and response shapes it exposes are not a stable
contract.

Authenticates using SAM_AUTH_TOKEN / SAM_PLATFORM_TOKEN, or the cached
OAuth token from sam auth login. The base URL is resolved from --url /
--target / --manifest, the SAM_WEBUI_URL env var, or, when exactly one
target is logged in, the single cache entry.

Body construction matches gh api: --field is typed (true/false/null and
numeric literals are emitted as JSON literals), --raw-field is always a
JSON string. Use key[]=val to accumulate arrays and key.sub=val to nest.`,
		Example: `  sam api /api/v2/platform/agents
  sam api /api/v2/platform/agents --jq '.data[].name'
  sam api /api/v2/platform/agents --paginate --jq '.data[].id'
  sam api -X POST /api/v1/projects --field name=demo --field description="ad-hoc test"
  sam api -X POST /api/v2/platform/agents --field name=demo --field description="A demo weather agent" --field systemPrompt="You are a demo weather agent. Answer basic questions about current conditions and forecasts for a given city or region." --field 'toolsets[]=web_tools' --field 'toolsets[]=builtin_artifact_tools'
  sam api -X DELETE /api/v1/sessions/$SID -i
  sam api /api/v2/platform/agents/$ID --input agent.json -X PUT
  sam api --target dev /api/v1/user`,
		Args:          cobra.RangeArgs(1, 2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.positional = args
			o.stdin = cmd.InOrStdin()
			o.stdout = cmd.OutOrStdout()
			o.stderr = cmd.ErrOrStderr()
			ctx := commandContext(cmd)
			code, err := runAPI(ctx, o)
			if err != nil {
				_, _ = fmt.Fprintln(o.stderr, "sam api:", err.Error())
			}
			if code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&o.method, "method", "X", "",
		"HTTP method (GET by default; POST when a body flag is present).")
	cmd.Flags().StringArrayVarP(&o.headers, "header", "H", nil,
		"Extra request header (repeatable); overrides defaults.")
	cmd.Flags().StringArrayVarP(&o.fields, "field", "F", nil,
		"Typed JSON field. true/false/null/numbers are coerced. key[]=v accumulates arrays; key.sub=v nests.")
	cmd.Flags().StringArrayVar(&o.rawFields, "raw-field", nil,
		"Raw string JSON field; identical to --field but the value is always a JSON string.")
	cmd.Flags().StringVar(&o.input, "input", "",
		"Read full request body from file path, @file, or - (stdin). Mutually exclusive with --field/--raw-field.")
	cmd.Flags().StringVar(&o.jqFilter, "jq", "",
		"Filter response with a gojq expression.")
	cmd.Flags().BoolVar(&o.raw, "raw", false,
		"Skip JSON parsing; write the response body to stdout verbatim.")
	cmd.Flags().BoolVarP(&o.include, "include", "i", false,
		"Print response status line and headers before the body.")
	cmd.Flags().BoolVar(&o.paginate, "paginate", false,
		"Follow meta.pagination.nextPage until exhausted.")
	cmd.Flags().StringVar(&o.target, "target", "",
		"Cached target name (sam auth login) to address.")
	cmd.Flags().StringVar(&o.url, "url", "",
		"Platform URL (or bare hostname). Overrides --target/--manifest.")
	cmd.Flags().StringVarP(&o.manifest, "manifest", "m", "",
		"Manifest file whose target (URL and auth) to use.")
	cmd.Flags().BoolVar(&o.insecure, "insecure", false,
		"Skip TLS verification. Also required to send a bearer token over plain http://.")
	cmd.Flags().BoolVarP(&o.verbose, "verbose", "v", false,
		"Print method, resolved URL, redacted headers, and timings to stderr.")

	return cmd
}

func runAPI(ctx context.Context, o *apiOpts) (int, error) {
	if o.stdout == nil {
		o.stdout = os.Stdout
	}
	if o.stderr == nil {
		o.stderr = os.Stderr
	}
	if o.stdin == nil {
		o.stdin = os.Stdin
	}

	method, pathArg, err := splitMethodAndPath(o.positional, o.method)
	if err != nil {
		return 2, err
	}

	if o.jqFilter != "" && o.raw {
		return 2, errors.New("--jq and --raw are mutually exclusive")
	}
	if o.input != "" && (len(o.fields) > 0 || len(o.rawFields) > 0) {
		return 2, errors.New("--input is mutually exclusive with --field/--raw-field")
	}

	body, err := buildRequestBody(o, mustParseFieldFlags)
	if err != nil {
		return 2, err
	}
	hasBody := body != nil

	if method == "" {
		if hasBody {
			method = http.MethodPost
		} else {
			method = http.MethodGet
		}
	}
	method = strings.ToUpper(method)

	if hasBody && (method == http.MethodGet || method == http.MethodHead) {
		return 2, fmt.Errorf("cannot send a body with %s", method)
	}

	tg, err := resolveAPITarget(ctx, o)
	if err != nil && !targetErrTolerable(err, o, pathArg) {
		return 3, err
	}
	baseURL := tg.URL

	fullURL, err := resolveURL(baseURL, pathArg)
	if err != nil {
		return 3, err
	}

	if safeErr := assertSafeURL(fullURL, o); safeErr != nil {
		return 2, safeErr
	}

	cred, err := resolveAPIToken(ctx, o, tg)
	if err != nil {
		return 3, err
	}
	if cred.FromManifest && cred.Token != "" && !sameOrigin(fullURL, tg.URL) {
		return 2, fmt.Errorf("refusing to send the manifest's credential to %s, which is not its target %s; use a path relative to the target, or pass --url", requestOrigin(fullURL), requestOrigin(tg.URL))
	}
	tok := cred.Token

	if tok != "" && strings.HasPrefix(strings.ToLower(fullURL), schemeHTTP) && !o.insecure {
		return 2, errors.New("refusing to send a bearer token over plain http:// — pass --insecure if you really mean it")
	}
	recordAddressedGateway(baseURL, "", tok, o.insecure, o.insecure)

	if o.insecure && strings.HasPrefix(strings.ToLower(fullURL), schemeHTTPS) {
		_, _ = fmt.Fprintln(o.stderr, "warning: --insecure: skipping TLS verification")
	}

	httpClient := o.httpClient
	if httpClient == nil {
		httpClient = newAPIHTTPClient(o.insecure)
	}

	headers, err := buildHeaders(o.headers, hasBody)
	if err != nil {
		return 2, err
	}

	call := apiCall{
		method:  method,
		url:     fullURL,
		token:   tok,
		headers: headers,
		body:    body,
	}
	if o.paginate {
		return runPaginate(ctx, httpClient, call, o)
	}
	return runSingle(ctx, httpClient, call, o)
}

func runSingle(ctx context.Context, client *http.Client, call apiCall, o *apiOpts) (int, error) {
	resp, raw, err := doRequest(ctx, client, call, o)
	if err != nil {
		return 3, err
	}

	if o.include {
		writeStatusAndHeaders(o.stdout, resp)
	}

	if resp.StatusCode >= 400 {
		_, _ = o.stdout.Write(raw)
		if !bytes.HasSuffix(raw, []byte("\n")) {
			_, _ = fmt.Fprintln(o.stdout)
		}
		writeErrorSummary(o.stderr, resp, raw)
		warnIfServerLacksV2(ctx, client, o.stderr, resp)
		if resp.StatusCode >= 500 {
			return 2, nil
		}
		return 1, nil
	}

	if err := writeResponseBody(o.stdout, raw, o.jqFilter, o.raw); err != nil {
		return 3, err
	}
	return 0, nil
}

func runPaginate(ctx context.Context, client *http.Client, call apiCall, o *apiOpts) (int, error) {
	if o.raw {
		return 2, errors.New("--paginate is incompatible with --raw")
	}

	var jqCode *gojq.Code
	if o.jqFilter != "" {
		var err error
		jqCode, err = compileJQ(o.jqFilter)
		if err != nil {
			return 2, err
		}
	}

	combined := paginatedAccumulator{}
	first := true

	for call.url != "" {
		resp, raw, err := doRequest(ctx, client, call, o)
		if err != nil {
			return 3, err
		}

		if first && o.include {
			writeStatusAndHeaders(o.stdout, resp)
		}
		first = false

		if resp.StatusCode >= 400 {
			_, _ = o.stdout.Write(raw)
			if !bytes.HasSuffix(raw, []byte("\n")) {
				_, _ = fmt.Fprintln(o.stdout)
			}
			writeErrorSummary(o.stderr, resp, raw)
			warnIfServerLacksV2(ctx, client, o.stderr, resp)
			if resp.StatusCode >= 500 {
				return 2, nil
			}
			return 1, nil
		}

		var page paginatedPage
		if jsonErr := json.Unmarshal(raw, &page); jsonErr != nil {
			return 3, fmt.Errorf("--paginate: parse response: %w", jsonErr)
		}
		if page.Meta == nil || page.Meta.Pagination == nil {
			return 2, errors.New("--paginate: response is not a PaginatedResponse (missing meta.pagination)")
		}

		if jqCode != nil {
			if jqErr := streamJQ(o.stdout, jqCode, raw); jqErr != nil {
				return 3, jqErr
			}
		} else {
			combined.add(page)
		}

		if page.Meta.Pagination.NextPage == nil {
			break
		}
		nextURL, err := withPageNumber(call.url, *page.Meta.Pagination.NextPage)
		if err != nil {
			return 3, fmt.Errorf("--paginate: rewrite pageNumber: %w", err)
		}
		call.url = nextURL
		call.method = http.MethodGet
		call.body = nil
	}

	if jqCode == nil {
		if err := writeIndentJSON(o.stdout, combined.snapshot()); err != nil {
			return 3, err
		}
	}
	return 0, nil
}

func doRequest(ctx context.Context, client *http.Client, call apiCall, o *apiOpts) (*http.Response, []byte, error) {
	var rdr io.Reader
	if call.body != nil {
		rdr = bytes.NewReader(call.body)
	}
	req, err := http.NewRequestWithContext(ctx, call.method, call.url, rdr)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	httputil.SetAPIVersion(req)
	maps.Copy(req.Header, call.headers)
	req.Header.Set(httputil.HeaderClientSurface, httputil.SurfaceCLI)
	if session := clianalytics.SessionID(); session != "" {
		req.Header.Set(httputil.HeaderClientSession, session)
	}
	if call.token != "" {
		req.Header.Set("Authorization", "Bearer "+call.token)
	}

	if o.verbose {
		logVerboseRequest(o.stderr, call.method, call.url, req.Header)
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	httputil.CLINotices.Record(resp)
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read response body: %w", err)
	}

	if o.verbose {
		_, _ = fmt.Fprintf(o.stderr, "< %s (%d bytes, %s)\n", resp.Status, len(raw), time.Since(start).Round(time.Millisecond))
	}

	return resp, raw, nil
}

func writeStatusAndHeaders(w io.Writer, resp *http.Response) {
	proto := resp.Proto
	if proto == "" {
		proto = "HTTP/1.1"
	}
	_, _ = fmt.Fprintf(w, "%s %s\n", proto, resp.Status)
	keys := make([]string, 0, len(resp.Header))
	for k := range resp.Header {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		for _, v := range resp.Header[k] {
			_, _ = fmt.Fprintf(w, "%s: %s\n", k, v)
		}
	}
	_, _ = fmt.Fprintln(w)
}

func warnIfServerLacksV2(ctx context.Context, client *http.Client, w io.Writer, resp *http.Response) {
	if resp.StatusCode != http.StatusNotFound || resp.Request == nil || !strings.HasPrefix(resp.Request.URL.Path, platform.APIV2Prefix+"/") {
		return
	}
	healthURL := *resp.Request.URL
	healthURL.Path, healthURL.RawQuery = platform.APIV2Prefix+"/health", ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL.String(), nil)
	if err != nil {
		return
	}
	probe, err := client.Do(req)
	if err != nil {
		return
	}
	_ = probe.Body.Close()
	if probe.StatusCode == http.StatusNotFound {
		_, _ = fmt.Fprintf(w, "sam api: %s\n", (&reconcile.NoV2Error{URL: resp.Request.URL.String()}).Error())
	}
}

func writeErrorSummary(w io.Writer, resp *http.Response, raw []byte) {
	var env struct {
		Message string `json:"message"`
		ErrorID string `json:"errorId"`
	}
	_ = json.Unmarshal(raw, &env)
	msg := env.Message
	if msg == "" {
		msg = strings.TrimSpace(http.StatusText(resp.StatusCode))
	}
	if env.ErrorID != "" {
		_, _ = fmt.Fprintf(w, "sam api: %s — %s (errorId=%s)\n", resp.Status, msg, env.ErrorID)
		return
	}
	_, _ = fmt.Fprintf(w, "sam api: %s — %s\n", resp.Status, msg)
}

func writeResponseBody(w io.Writer, raw []byte, jqFilter string, rawMode bool) error {
	if rawMode {
		_, err := w.Write(raw)
		return err
	}
	if jqFilter == "" {
		_, err := w.Write(raw)
		if err == nil && len(raw) > 0 && !bytes.HasSuffix(raw, []byte("\n")) {
			_, _ = w.Write([]byte("\n"))
		}
		return err
	}
	code, err := compileJQ(jqFilter)
	if err != nil {
		return err
	}
	return streamJQ(w, code, raw)
}

func compileJQ(filter string) (*gojq.Code, error) {
	q, err := gojq.Parse(filter)
	if err != nil {
		return nil, fmt.Errorf("--jq: parse %q: %w", filter, err)
	}
	code, err := gojq.Compile(q)
	if err != nil {
		return nil, fmt.Errorf("--jq: compile %q: %w", filter, err)
	}
	return code, nil
}

func streamJQ(w io.Writer, code *gojq.Code, raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("--jq: response is not JSON: %w (use --raw to bypass)", err)
	}
	iter := code.Run(v)
	for {
		result, ok := iter.Next()
		if !ok {
			break
		}
		if err, isErr := result.(error); isErr {
			return fmt.Errorf("--jq: %w", err)
		}
		if s, ok := result.(string); ok {
			_, _ = fmt.Fprintln(w, s)
			continue
		}
		out, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("--jq: marshal result: %w", err)
		}
		_, _ = fmt.Fprintln(w, string(out))
	}
	return nil
}

func writeIndentJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

type paginatedPage struct {
	Data json.RawMessage `json:"data"`
	Meta *paginatedMeta  `json:"meta"`
	Rest map[string]any  `json:"-"`
}

type paginatedMeta struct {
	Pagination *paginatedPagination `json:"pagination"`
}

type paginatedPagination struct {
	PageNumber int  `json:"pageNumber"`
	Count      int  `json:"count"`
	PageSize   int  `json:"pageSize"`
	NextPage   *int `json:"nextPage"`
	TotalPages int  `json:"totalPages"`
}

type paginatedAccumulator struct {
	items      []json.RawMessage
	pageSize   int
	totalPages int
}

func (a *paginatedAccumulator) add(p paginatedPage) {
	var arr []json.RawMessage
	if len(p.Data) > 0 {
		_ = json.Unmarshal(p.Data, &arr)
	}
	a.items = append(a.items, arr...)
	if p.Meta != nil && p.Meta.Pagination != nil {
		if a.pageSize == 0 {
			a.pageSize = p.Meta.Pagination.PageSize
		}
		if p.Meta.Pagination.TotalPages > a.totalPages {
			a.totalPages = p.Meta.Pagination.TotalPages
		}
	}
}

func (a *paginatedAccumulator) snapshot() map[string]any {
	pageSize := a.pageSize
	if pageSize == 0 {
		pageSize = len(a.items)
	}
	totalPages := a.totalPages
	if totalPages == 0 && len(a.items) > 0 {
		totalPages = 1
	}
	return map[string]any{
		"data": a.itemsValue(),
		"meta": map[string]any{
			"pagination": map[string]any{
				"pageNumber": 1,
				"count":      len(a.items),
				"pageSize":   pageSize,
				"nextPage":   nil,
				"totalPages": totalPages,
			},
		},
	}
}

func (a *paginatedAccumulator) itemsValue() []json.RawMessage {
	if a.items == nil {
		return []json.RawMessage{}
	}
	return a.items
}

func splitMethodAndPath(args []string, methodFlag string) (string, string, error) {
	switch len(args) {
	case 1:
		return methodFlag, args[0], nil
	case 2:
		if methodFlag != "" && !strings.EqualFold(methodFlag, args[0]) {
			return "", "", fmt.Errorf("conflicting methods: positional %q vs --method %q", args[0], methodFlag)
		}
		m := strings.ToUpper(args[0])
		if !isHTTPMethod(m) {
			return "", "", fmt.Errorf("unknown HTTP method %q", args[0])
		}
		return m, args[1], nil
	default:
		return "", "", errors.New("missing PATH argument")
	}
}

func isHTTPMethod(s string) bool {
	switch s {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return true
	}
	return false
}

func buildRequestBody(o *apiOpts, parseFields func(typed, raw []string) ([]fieldFlag, error)) ([]byte, error) {
	if o.input != "" {
		return readInputBody(o.input, o.stdin)
	}
	if len(o.fields) == 0 && len(o.rawFields) == 0 {
		return nil, nil
	}
	flags, err := parseFields(o.fields, o.rawFields)
	if err != nil {
		return nil, err
	}
	return buildJSONBody(flags)
}

func readInputBody(input string, stdin io.Reader) ([]byte, error) {
	if input == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("--input: read stdin: %w", err)
		}
		return data, nil
	}
	path := strings.TrimPrefix(input, "@")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--input: %w", err)
	}
	return data, nil
}

type fieldFlag struct {
	key     string
	value   string
	raw     bool
	isArray bool
}

func mustParseFieldFlags(typed, raw []string) ([]fieldFlag, error) {
	out := make([]fieldFlag, 0, len(typed)+len(raw))
	for _, s := range typed {
		ff, err := parseFieldFlag(s, false)
		if err != nil {
			return nil, err
		}
		out = append(out, ff)
	}
	for _, s := range raw {
		ff, err := parseFieldFlag(s, true)
		if err != nil {
			return nil, err
		}
		out = append(out, ff)
	}
	return out, nil
}

func parseFieldFlag(s string, raw bool) (fieldFlag, error) {
	eq := strings.Index(s, "=")
	if eq <= 0 {
		return fieldFlag{}, fmt.Errorf("field %q: expected key=value", s)
	}
	key := s[:eq]
	val := s[eq+1:]
	isArray := false
	if strings.HasSuffix(key, "[]") {
		isArray = true
		key = strings.TrimSuffix(key, "[]")
	}
	if strings.Contains(key, "[") || strings.Contains(key, "]") {
		return fieldFlag{}, fmt.Errorf("field %q: only a single trailing [] is supported", s)
	}
	if key == "" {
		return fieldFlag{}, fmt.Errorf("field %q: empty key", s)
	}
	return fieldFlag{key: key, value: val, raw: raw, isArray: isArray}, nil
}

var jsonNumberRE = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

func coerceValue(value string, raw bool) any {
	if raw {
		return value
	}
	switch value {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if jsonNumberRE.MatchString(value) {
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			return n
		}
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
	}
	return value
}

func buildJSONBody(flags []fieldFlag) ([]byte, error) {
	root := map[string]any{}
	for _, ff := range flags {
		parts := strings.Split(ff.key, ".")
		if slices.Contains(parts, "") {
			return nil, fmt.Errorf("field %q: empty key segment", ff.key)
		}
		if err := setPath(root, parts, ff); err != nil {
			return nil, err
		}
	}
	return json.Marshal(root)
}

func setPath(obj map[string]any, parts []string, ff fieldFlag) error {
	last := len(parts) - 1
	for i, p := range parts[:last] {
		cur, ok := obj[p]
		if !ok {
			next := map[string]any{}
			obj[p] = next
			obj = next
			continue
		}
		next, isMap := cur.(map[string]any)
		if !isMap {
			return fmt.Errorf("field %q: cannot nest under non-object key %q", ff.key, strings.Join(parts[:i+1], "."))
		}
		obj = next
	}

	leaf := parts[last]
	val := coerceValue(ff.value, ff.raw)
	existing, exists := obj[leaf]
	if ff.isArray {
		if !exists {
			obj[leaf] = []any{val}
			return nil
		}
		arr, isArr := existing.([]any)
		if !isArr {
			return fmt.Errorf("field %q: cannot append [] to non-array existing key", ff.key)
		}
		obj[leaf] = append(arr, val)
		return nil
	}
	if exists {
		if _, isArr := existing.([]any); isArr {
			return fmt.Errorf("field %q: cannot overwrite array key with scalar (use []= to append)", ff.key)
		}
	}
	obj[leaf] = val
	return nil
}

func buildHeaders(rawHeaders []string, hasBody bool) (http.Header, error) {
	h := http.Header{}
	h.Set("Accept", "application/json")
	if hasBody {
		h.Set("Content-Type", "application/json")
	}
	for _, raw := range rawHeaders {
		idx := strings.Index(raw, ":")
		if idx <= 0 {
			return nil, fmt.Errorf("header %q: expected Name: value", raw)
		}
		name := strings.TrimSpace(raw[:idx])
		value := strings.TrimSpace(raw[idx+1:])
		if name == "" {
			return nil, fmt.Errorf("header %q: empty name", raw)
		}
		h.Set(name, value)
	}
	return h, nil
}

func logVerboseRequest(w io.Writer, method, fullURL string, h http.Header) {
	_, _ = fmt.Fprintf(w, "> %s %s\n", method, fullURL)
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if isRedactedHeader(k) {
			_, _ = fmt.Fprintf(w, "> %s: <redacted>\n", k)
			continue
		}
		for _, v := range h[k] {
			_, _ = fmt.Fprintf(w, "> %s: %s\n", k, v)
		}
	}
}

func isRedactedHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "cookie", "set-cookie", "proxy-authorization":
		return true
	}
	return false
}

func newAPIHTTPClient(insecure bool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = insecureTLSConfig()
	}
	return &http.Client{
		Transport: tr,
		Timeout:   2 * time.Minute,
	}
}

func insecureTLSConfig() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true} //nolint:gosec // NOSONAR
}

func resolveAPITarget(ctx context.Context, o *apiOpts) (target.Target, error) {
	if o.resolveBase != nil {
		url, err := o.resolveBase(ctx, o)
		return target.Target{URL: url}, err
	}
	return resolveGatewayTarget(ctx, gatewayTargetRef{
		URL:      o.url,
		Target:   o.target,
		Manifest: o.manifest,
	}, "")
}

func targetErrTolerable(err error, o *apiOpts, pathArg string) bool {
	if !isAbsoluteHTTP(pathArg) {
		return false
	}
	return o.manifest == "" || errors.Is(err, errManifestTargetURLEmpty)
}

func resolveAPIToken(ctx context.Context, o *apiOpts, tg target.Target) (gatewayCredential, error) {
	if o.resolveTok != nil && tg.Auth.Type == "" {
		if tok := envFirstNonEmpty("SAM_AUTH_TOKEN", "SAM_PLATFORM_TOKEN"); tok != "" {
			return gatewayCredential{Token: tok}, nil
		}
		return gatewayCredential{Token: o.resolveTok(ctx, tg.URL, "")}, nil
	}
	return resolveGatewayCredential(ctx, tg)
}

func normaliseBaseURL(v string) string {
	v = strings.TrimRight(strings.TrimSpace(v), "/")
	low := strings.ToLower(v)
	if strings.HasPrefix(low, schemeHTTP) || strings.HasPrefix(low, schemeHTTPS) {
		return v
	}
	return schemeHTTPS + v
}

func isAbsoluteHTTP(s string) bool {
	low := strings.ToLower(s)
	return strings.HasPrefix(low, schemeHTTP) || strings.HasPrefix(low, schemeHTTPS)
}

func resolveURL(base, ref string) (string, error) {
	if isAbsoluteHTTP(ref) {
		return ref, nil
	}
	if base == "" {
		return "", errors.New("relative path requires a base URL")
	}
	baseU, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}
	refU, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("parse path: %w", err)
	}
	return baseU.ResolveReference(refU).String(), nil
}

func withPageNumber(u string, n int) (string, error) {
	parsed, err := url.Parse(u)
	if err != nil {
		return "", fmt.Errorf("parse URL: %w", err)
	}
	q := parsed.Query()
	q.Set("pageNumber", strconv.Itoa(n))
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

func assertSafeURL(fullURL string, o *apiOpts) error {
	if strings.HasPrefix(strings.ToLower(fullURL), schemeHTTP) && !o.insecure {
		return fmt.Errorf("refusing plain http:// URL %q without --insecure", fullURL)
	}
	return nil
}

func envFirstNonEmpty(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}
