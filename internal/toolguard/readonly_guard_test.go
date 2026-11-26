// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolguard

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const repoRoot = "../.."

var readOnlyRoots = []string{"pkg/tools", "internal/skill", "pkg/harnesstools", "tools/go"}

var writeCalls = []string{
	"SaveArtifact", "SaveArtifactWithMetadata", "SaveWithMetadata", "Save",
	"saveActivityMarkdown", "SendEmail", "SaveArtifactVersion",
}

var fileWriteCalls = []string{"WriteFile", "Create"}

var osMutationCalls = []string{"OpenFile", "Remove", "RemoveAll", "Rename"}

var registrationCalls = []string{"Hold", "Register", "RegisterSkillTools", "RegisterOneToolEntry", "AddClient", "DiscoverTools"}

var networkCalls = []string{"NewRequest", "NewRequestWithContext"}

var networkExemptions = map[string]string{
	"pkg/tools/DatadogQueryLogsTool":        "sends the query to the one configured Datadog host",
	"pkg/tools/DatadogGetErrorsTool":        "sends the query to the one configured Datadog host",
	"pkg/tools/CorrelateTaskWithLogsTool":   "sends the query to the one configured Datadog host",
	"tools/go/bamboohr_profile_provider/*":  "reads the one configured BambooHR host",
	"pkg/tools/ValidateComponentConfigTool": "looks a connector up by name over a POST that only reads",
}

var localWorkExemptions = map[string]string{
	"tools/go/ffmpeg/ffmpegtool/ffprobe":                        "runs ffprobe on its input copied into a temp directory it removes on return",
	"tools/go/pdf_extract_text/pdfextracttext/pdf_extract_text": "extracts from its input copied into a temp file it removes on return",
	"tools/go/sam_coder/coder/sam_coder":                        "unpacks its input bundle into a temp directory it removes on return and runs git and a syntax checker inside it",
}

type violation struct {
	root, construct string
	reachesNetwork  bool
	reachesLocal    bool
	pos             token.Position
}

func (v violation) String() string {
	return fmt.Sprintf("%s: %s reachable from the read_only tool %s", v.pos, v.construct, v.root)
}

type pkgSource struct {
	dir   string
	fset  *token.FileSet
	funcs map[string][]*ast.FuncDecl
	files []*ast.File
}

func pkgFromSource(t *testing.T, dir, source string) *pkgSource {
	t.Helper()
	pkg := &pkgSource{dir: dir, fset: token.NewFileSet(), funcs: map[string][]*ast.FuncDecl{}}
	parsed, err := parser.ParseFile(pkg.fset, "fixture.go", source, 0)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	pkg.files = append(pkg.files, parsed)
	for _, decl := range parsed.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			pkg.funcs[fn.Name.Name] = append(pkg.funcs[fn.Name.Name], fn)
		}
	}
	return pkg
}

func loadPackages(t *testing.T) []*pkgSource {
	t.Helper()
	var pkgs []*pkgSource
	for _, root := range readOnlyRoots {
		err := filepath.WalkDir(filepath.Join(repoRoot, root), func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return err
			}
			entries, readErr := filepath.Glob(filepath.Join(path, "*.go"))
			if readErr != nil || len(entries) == 0 {
				return readErr
			}
			pkg := &pkgSource{dir: filepath.ToSlash(strings.TrimPrefix(path, repoRoot+"/")), fset: token.NewFileSet(), funcs: map[string][]*ast.FuncDecl{}}
			for _, file := range entries {
				if strings.HasSuffix(file, "_test.go") {
					continue
				}
				parsed, parseErr := parser.ParseFile(pkg.fset, file, nil, 0)
				if parseErr != nil {
					return parseErr
				}
				pkg.files = append(pkg.files, parsed)
				for _, decl := range parsed.Decls {
					if fn, ok := decl.(*ast.FuncDecl); ok {
						pkg.funcs[fn.Name.Name] = append(pkg.funcs[fn.Name.Name], fn)
					}
				}
			}
			pkgs = append(pkgs, pkg)
			return nil
		})
		if err != nil {
			t.Fatalf("scanning %s: %v", root, err)
		}
	}
	return pkgs
}

type readOnlyRoot struct {
	name string
	body []ast.Node
}

func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

func embedsReadOnlyTool(spec *ast.TypeSpec) bool {
	structType, ok := spec.Type.(*ast.StructType)
	if !ok {
		return false
	}
	for _, field := range structType.Fields.List {
		if len(field.Names) != 0 {
			continue
		}
		if sel, ok := field.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "ReadOnlyTool" {
			return true
		}
	}
	return false
}

func declaresReadOnlyEffect(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		option, ok := arg.(*ast.CallExpr)
		if !ok {
			continue
		}
		if sel, ok := option.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "WithEffect" || len(option.Args) != 1 {
			continue
		}
		if sel, ok := option.Args[0].(*ast.SelectorExpr); ok && sel.Sel.Name == "EffectReadOnly" {
			return true
		}
	}
	return false
}

func findRoots(pkg *pkgSource) []readOnlyRoot {
	var roots []readOnlyRoot
	readOnlyTypes := map[string]bool{}
	for _, file := range pkg.files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				if typeSpec, ok := spec.(*ast.TypeSpec); ok && embedsReadOnlyTool(typeSpec) {
					readOnlyTypes[typeSpec.Name.Name] = true
				}
			}
		}
	}
	for name := range readOnlyTypes {
		for _, fn := range pkg.funcs["Execute"] {
			if receiverName(fn) == name && fn.Body != nil {
				roots = append(roots, readOnlyRoot{name: pkg.dir + "/" + name, body: []ast.Node{fn.Body}})
			}
		}
	}
	for _, file := range pkg.files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 3 || !declaresReadOnlyEffect(call) {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "NewTool" {
				return true
			}
			label := pkg.dir + "/" + toolLabel(call.Args[0])
			switch handler := call.Args[2].(type) {
			case *ast.Ident:
				for _, fn := range pkg.funcs[handler.Name] {
					if fn.Body != nil {
						roots = append(roots, readOnlyRoot{name: label, body: []ast.Node{fn.Body}})
					}
				}
			case *ast.FuncLit:
				roots = append(roots, readOnlyRoot{name: label, body: []ast.Node{handler.Body}})
			}
			return true
		})
	}
	return roots
}

func toolLabel(expr ast.Expr) string {
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		if name, err := strconv.Unquote(lit.Value); err == nil {
			return name
		}
	}
	return fmt.Sprintf("%T", expr)
}

func calleeName(call *ast.CallExpr) (name string, qualifier string) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name, ""
	case *ast.SelectorExpr:
		if ident, ok := fun.X.(*ast.Ident); ok {
			return fun.Sel.Name, ident.Name
		}
		return fun.Sel.Name, ""
	}
	return "", ""
}

func isDataObjectLiteral(lit *ast.CompositeLit) bool {
	switch typ := lit.Type.(type) {
	case *ast.Ident:
		return typ.Name == "DataObject"
	case *ast.SelectorExpr:
		return typ.Sel.Name == "DataObject"
	}
	return false
}

func scan(pkg *pkgSource, root readOnlyRoot) []violation {
	var found []violation
	visited := map[*ast.FuncDecl]bool{}
	queue := slices.Clone(root.body)
	tools := strings.HasPrefix(pkg.dir, "tools/go")
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		ast.Inspect(node, func(n ast.Node) bool {
			report := func(construct string) {
				found = append(found, violation{root: root.name, construct: construct, pos: pkg.fset.Position(n.Pos())})
			}
			reportLocal := func(construct string) {
				found = append(found, violation{root: root.name, construct: construct, reachesLocal: true, pos: pkg.fset.Position(n.Pos())})
			}
			reportNetwork := func(construct string) {
				found = append(found, violation{root: root.name, construct: construct, reachesNetwork: true, pos: pkg.fset.Position(n.Pos())})
			}
			switch typed := n.(type) {
			case *ast.CompositeLit:
				if isDataObjectLiteral(typed) {
					if tools {
						report("a DataObject literal")
					}
					for _, elt := range typed.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok {
							if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Name" {
								report("a DataObject with a Name")
							}
						}
					}
				}
			case *ast.SelectorExpr:
				if ident, ok := typed.X.(*ast.Ident); ok && ident.Name == "http" && typed.Sel.Name == "DefaultClient" {
					reportNetwork("http.DefaultClient")
				}
			case *ast.CallExpr:
				name, qualifier := calleeName(typed)
				switch {
				case slices.Contains(writeCalls, name), slices.Contains(fileWriteCalls, name) && !tools:
					report("a call to " + name)
				case name == "Delete" && len(typed.Args) >= 2:
					report("a call to Delete with more than a key")
				case slices.Contains(registrationCalls, name):
					report("a call to " + name)
				case qualifier == "os" && slices.Contains(osMutationCalls, name):
					reportLocal("a call to os." + name)
				case qualifier == "exec":
					reportLocal("a call to exec." + name)
				case slices.Contains(networkCalls, name) && !tools:
					reportNetwork("a call to " + name)
				case qualifier == "http" && (name == "Get" || name == "Post"):
					reportNetwork("a call to http." + name)
				case name == "Custom" && len(typed.Args) >= 3:
					if lit, ok := typed.Args[2].(*ast.BasicLit); ok && lit.Value != `"get"` {
						reportNetwork("a control request with the verb " + lit.Value)
					}
				}
				for _, fn := range pkg.funcs[name] {
					if fn.Body != nil && !visited[fn] {
						visited[fn] = true
						queue = append(queue, fn.Body)
					}
				}
			}
			return true
		})
	}
	return found
}

func exempt(rootName string) bool {
	if _, ok := networkExemptions[rootName]; ok {
		return true
	}
	dir := rootName[:strings.LastIndex(rootName, "/")]
	_, ok := networkExemptions[dir+"/*"]
	return ok
}

func TestTripwire_ReviewedEffectTablesAreTheSourceOfTruth_NoReadOnlyToolShowsACheapSignOfBreakingTheRule(t *testing.T) {
	t.Parallel()
	roots := 0
	for _, pkg := range loadPackages(t) {
		for _, root := range findRoots(pkg) {
			roots++
			exemptFromNetwork := exempt(root.name)
			_, exemptFromLocal := localWorkExemptions[root.name]
			for _, v := range scan(pkg, root) {
				if v.reachesNetwork && exemptFromNetwork || v.reachesLocal && exemptFromLocal {
					continue
				}
				t.Error(v)
			}
		}
	}
	if roots < 40 {
		t.Fatalf("found only %d read_only tools to check; the scan has stopped seeing them", roots)
	}
}

func TestTripwire_FlagsEachCheapSignItListsAndLeavesPlainCodeAlone(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, dir, statement, want string
	}{
		{"saving an artifact", "pkg/tools", `store.SaveArtifact(ctx, a, b)`, "a call to SaveArtifact"},
		{"saving a version", "pkg/tools", `store.SaveArtifactVersion(ctx, a, b)`, "a call to SaveArtifactVersion"},
		{"deleting an artifact", "pkg/tools", `store.Delete(ctx, a, b, c, name)`, "a call to Delete with more than a key"},
		{"registering a tool", "pkg/tools", `registry.Register(tool)`, "a call to Register"},
		{"opening a file for writing", "pkg/tools", `os.OpenFile(path, 0, 0)`, "a call to os.OpenFile"},
		{"removing a file", "pkg/tools", `os.Remove(path)`, "a call to os.Remove"},
		{"running a program", "pkg/tools", `exec.Command(path)`, "a call to exec.Command"},
		{"using the default HTTP client", "pkg/tools", `http.DefaultClient.Do(req)`, "http.DefaultClient"},
		{"building a request", "pkg/tools", `http.NewRequest("GET", u, nil)`, "a call to NewRequest"},
		{"fetching with http.Get", "tools/go/x", `http.Get(u)`, "a call to http.Get"},
		{"a data object with a name", "tools/go/x", `_ = sdk.DataObject{Name: "out.txt"}`, "a DataObject with a Name"},
		{"deleting one key from an in-memory map", "pkg/tools", `counters.Delete(key)`, ""},
		{"reading a file", "pkg/tools", `os.ReadFile(path)`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := "package fixture\n\nfunc run() {\n\t" + tc.statement + "\n}\n"
			pkg := pkgFromSource(t, tc.dir, source)
			found := scan(pkg, readOnlyRoot{name: tc.dir + "/tool", body: []ast.Node{pkg.funcs["run"][0].Body}})
			flagged := slices.ContainsFunc(found, func(v violation) bool { return strings.Contains(v.construct, tc.want) })
			if tc.want == "" && len(found) != 0 || tc.want != "" && !flagged {
				t.Fatalf("scanning %q found %v, want construct %q", tc.statement, found, tc.want)
			}
		})
	}
}
