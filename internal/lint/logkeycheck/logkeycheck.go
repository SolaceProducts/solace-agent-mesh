// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package logkeycheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

var Analyzer = &analysis.Analyzer{
	Name:     "logkeycheck",
	Doc:      "flags snake_case slog keys and reserved Datadog-remapped keys (status); allows uppercase initialisms, unit metric suffixes, RFC JWT/OAuth keys, and //logkeys:allow",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

const allowComment = "//logkeys:allow"

const diagFormat = "snake_case slog key %q: use camelCase with uppercase initialisms (e.g. taskID, baseURL); unit metric keys (_ms/_s/_bytes) and RFC JWT/OAuth keys are exempt; annotate a genuine A2A-wire key with //logkeys:allow"

var reservedKeys = map[string]bool{"status": true}

const reservedDiagFormat = "reserved slog key %q is remapped by Datadog as the log severity (it overrides the real level — e.g. status:403 shows as info, status:\"completed\" as critical); rename to a domain-specific key (e.g. httpStatus, taskStatus); annotate with //logkeys:allow only if deliberate"

var metricSuffixes = []string{
	"_ms", "_s", "_sec", "_seconds", "_ns", "_us",
	"_bytes", "_kb", "_mb", "_gb",
}

var protocolAllow = map[string]bool{
	"component": true, "logger_name": true,

	"iss": true, "kid": true, "sub": true, "aud": true, "exp": true,
	"iat": true, "nbf": true, "jti": true, "azp": true,
	"grant_type": true, "grant_types_supported": true, "token_type": true,
	"access_token": true, "refresh_token": true, "id_token": true,
	"expires_in": true, "scope": true,
	"code_challenge": true, "code_challenge_method": true,
	"code_challenge_methods_supported": true, "response_types_supported": true,
	"jwks_uri": true, "token_endpoint": true, "token_endpoint_auth_method": true,
	"authorization_endpoint": true, "registration_endpoint": true,
	"resource_url": true, "client_redirect_uri": true,
}

var loggerMethods = map[string]bool{
	"Debug": true, "Info": true, "Warn": true, "Error": true,
	"DebugContext": true, "InfoContext": true, "WarnContext": true, "ErrorContext": true,
	"Log": true, "LogAttrs": true, "With": true,
}

var pkgFuncs = map[string]bool{
	"Debug": true, "Info": true, "Warn": true, "Error": true,
	"DebugContext": true, "InfoContext": true, "WarnContext": true, "ErrorContext": true,
	"Log": true, "LogAttrs": true, "With": true, "Group": true,
	"String": true, "Int": true, "Int64": true, "Uint": true, "Uint64": true,
	"Float64": true, "Bool": true, "Any": true, "Duration": true, "Time": true,
}

var attrConstructors = map[string]bool{
	"String": true, "Int": true, "Int64": true, "Uint": true, "Uint64": true,
	"Float64": true, "Bool": true, "Any": true, "Duration": true, "Time": true,
	"Group": true,
}

func run(pass *analysis.Pass) (any, error) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	allowLines := collectAllowLines(pass)

	insp.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		call := n.(*ast.CallExpr)
		if isTestFile(pass, call.Pos()) {
			return
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return
		}
		method := sel.Sel.Name
		start, ok := keyStart(pass, sel, method)
		if !ok {
			return
		}
		for i := start; i < len(call.Args); i += 2 {
			lit, ok := call.Args[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			key, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			format, bad := violationFormat(key)
			if !bad {
				continue
			}
			pos := pass.Fset.Position(lit.Pos())
			if allowLines[allowKey{file: pos.Filename, line: pos.Line}] {
				continue
			}
			pass.Reportf(lit.Pos(), format, key)
		}
	})

	return nil, nil
}

func keyStart(pass *analysis.Pass, sel *ast.SelectorExpr, method string) (int, bool) {
	if isSlogPackageCall(pass, sel.X) {
		if !pkgFuncs[method] {
			return 0, false
		}
		return pkgFuncKeyStart(method), true
	}
	if isSlogLoggerCall(pass, sel.X) {
		if !loggerMethods[method] {
			return 0, false
		}
		return loggerKeyStart(method), true
	}
	return 0, false
}

func pkgFuncKeyStart(method string) int {
	switch {
	case attrConstructors[method]:
		return 0
	case method == "With":
		return 0
	case method == "LogAttrs":
		return noStringKeys
	case method == "Log":
		return 3
	case strings.HasSuffix(method, "Context"):
		return 2
	default:
		return 1
	}
}

const noStringKeys = 1 << 30

func loggerKeyStart(method string) int {
	switch {
	case method == "With":
		return 0
	case method == "Log":
		return 3
	case method == "LogAttrs":
		return noStringKeys
	case strings.HasSuffix(method, "Context"):
		return 2
	default:
		return 1
	}
}

func isSlogPackageCall(pass *analysis.Pass, x ast.Expr) bool {
	id, ok := x.(*ast.Ident)
	if !ok {
		return false
	}
	obj, ok := pass.TypesInfo.Uses[id]
	if !ok {
		return false
	}
	pkgName, ok := obj.(*types.PkgName)
	if !ok {
		return false
	}
	return pkgName.Imported().Path() == "log/slog"
}

func isSlogLoggerCall(pass *analysis.Pass, x ast.Expr) bool {
	t := pass.TypesInfo.TypeOf(x)
	if t == nil {
		return false
	}
	ptr, ok := t.(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := ptr.Elem().(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Name() != "Logger" {
		return false
	}
	pkg := obj.Pkg()
	return pkg != nil && pkg.Path() == "log/slog"
}

func isTestFile(pass *analysis.Pass, pos token.Pos) bool {
	return strings.HasSuffix(pass.Fset.Position(pos).Filename, "_test.go")
}

func violationFormat(key string) (string, bool) {
	if reservedKeys[key] {
		return reservedDiagFormat, true
	}
	if violates(key) {
		return diagFormat, true
	}
	return "", false
}

func violates(key string) bool {
	if !strings.Contains(key, "_") {
		return false
	}
	if protocolAllow[key] {
		return false
	}
	for _, suf := range metricSuffixes {
		if strings.HasSuffix(key, suf) {
			return false
		}
	}
	return true
}

type allowKey struct {
	file string
	line int
}

func collectAllowLines(pass *analysis.Pass) map[allowKey]bool {
	out := map[allowKey]bool{}
	for _, f := range pass.Files {
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if strings.Contains(c.Text, allowComment) {
					pos := pass.Fset.Position(c.Pos())
					out[allowKey{file: pos.Filename, line: pos.Line}] = true
				}
			}
		}
	}
	return out
}
