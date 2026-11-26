// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const modulePath = "github.com/SolaceProducts/solace-agent-mesh"

var llmCallConstructors = map[string][]string{
	modulePath + "/internal/llm":                {"NewBifrostClient"},
	modulePath + "/internal/decision":           {"NewTransport"},
	modulePath + "/internal/modelclient":        {"Build"},
	modulePath + "/internal/decision/providers": {"NewEvaluator"},
	modulePath + "/internal/decision/aliased":   {"New"},
	modulePath + "/internal/decision/typesafe":  {"New"},
	modulePath + "/internal/decision/openai":    {"New"},
	modulePath + "/internal/decision/gemini":    {"New"},
	modulePath + "/internal/decision/llmeval":   {"New"},
}

type llmCallSite struct {
	constructions int
	reason        string
}

var llmCallSites = map[string]llmCallSite{
	"internal/awe/bindings/registry.go:buildClient": {1, "binding clients; the agent (Agent.llmClient), " +
		"the STR (Worker.newToolLLMCallback) when the invoke carries Spend (agent callers only), " +
		"the gateway (resolveGeneralLLM) and the platform wrap them in NewMeteredClient per call"},
	"internal/bootstrap/llm.go:buildLLMOptions": {1, "the boot-time default client (Services.LLM); " +
		"recorded by the same callers as the binding clients"},
	"internal/platform/admin/dynamic_llm_provider.go:DynamicLLMProvider.applyRawConfig": {1, "rebuilds the default client " +
		"behind Services.LLM; recorded by the same callers as the binding clients"},
	"internal/modelclient/modelclient.go:Build": {1, "the client of a model configuration, behind modelclient.Build, " +
		"whose callers are listed here"},
	"internal/llm/bifrost.go:BifrostClient.overrideClient": {1, "a per-request sub-client inside BifrostClient; " +
		"recorded by the wrapper around the outer client"},
	"internal/decision/providers/providers.go:known": {3, "the provider registry behind providers.NewEvaluator, " +
		"whose callers are listed here"},
	"internal/decision/typesafe/client.go:New": {1, "the transport behind typesafe.New, reached through " +
		"providers.NewEvaluator, whose callers are listed here"},
	"internal/decision/openai/client.go:New": {1, "the transport behind openai.New, reached through " +
		"providers.NewEvaluator, whose callers are listed here"},
	"internal/decision/gemini/client.go:New": {1, "the transport behind gemini.New, reached through " +
		"providers.NewEvaluator, whose callers are listed here"},

	"internal/platform/admin/ai_assistant_service.go:AIAssistantService.resolveLLMClientFromConfigSvc": {1, "agent generator; " +
		"GenerateAgent wraps it in NewMeteredClient (agent_generator)"},
	"internal/platform/eval/scorers.go:runJudgeClient.ownedClient": {1, "eval judge; wrapped in NewMeteredClient (eval_judge)"},
	"internal/platform/eval/decision_scorer.go:runDecisionClients.build": {1, "eval decision evaluator; " +
		"decisionCase.score wraps it in NewMeteredEvaluator per call (eval_judge)"},
	"internal/platform/llmtest/probe.go:ProbeError": {1, "chat model connection test, which Probe renders into a " +
		"sentence and /api/v2 classifies itself; recorded when the caller passes a SpendRecording " +
		"(model_test), as the /api/v2 model lookup handler does"},
	"internal/platform/llmtest/decision_probe.go:ProbeDecisionError": {1, "decision model connection test, which " +
		"ProbeDecision renders into a sentence for v1 and /api/v2 classifies itself; recorded when the caller " +
		"passes a SpendRecording (model_test), as the admin ModelConfigHandler and the /api/v2 model lookup " +
		"handler both do. The community POST /api/v1/platform/models/test passes none and records nothing; it is " +
		"mounted only when no RegisterRoutes extension is set, which no shipped binary does"},
	"internal/workflow/decision_instance.go:WorkflowInstance.initDecisions": {2, "a decision node's inline endpoint " +
		"and its aliased model; DAGExecutor.metered wraps either in NewMeteredEvaluator per call (decision)"},
	"internal/decision/aliased/aliased.go:Evaluator.clientFor": {1, "a decision node's aliased model; " +
		"DAGExecutor.metered wraps the aliased Evaluator in NewMeteredEvaluator per call (decision)"},

	"internal/settings/models.go:TestConnection": {1, "first-run connection test in the desktop app and CLI, " +
		"before any platform, ledger or broker exists"},
}

func TestEveryLLMCallSiteIsAccountedFor(t *testing.T) {
	found := scannedLLMCallSites(t)
	var unlisted []string
	for site, constructions := range found {
		listed, ok := llmCallSites[site]
		switch {
		case !ok:
			unlisted = append(unlisted, site)
		case listed.constructions != constructions:
			t.Errorf("%s builds an LLM client or decision evaluator %d times; llmCallSites lists %d (%s). "+
				"Record any new construction, then update the count and reason.",
				site, constructions, listed.constructions, listed.reason)
		}
	}
	slices.Sort(unlisted)
	if len(unlisted) > 0 {
		t.Errorf("LLM clients or decision evaluators are built in:\n  %s\n\n"+
			"A call through one records no spend unless it is wrapped in spend.NewMeteredClient or "+
			"spend.NewMeteredEvaluator and carries an llm.SpendAttribution (llm.ChatRequest.Spend, llmeval.WithSpend). "+
			"Record its calls, then add the function to llmCallSites saying how they reach the ledger; or add it with "+
			"the reason they are not recorded. This check covers direct construction only: a caller going through a "+
			"shared client (Services.LLM, bindings ResolveOrFetch) is not checked.",
			strings.Join(unlisted, "\n  "))
	}
}

func TestLLMCallSiteAllowlistHasNoStaleEntries(t *testing.T) {
	found := scannedLLMCallSites(t)
	for site, listed := range llmCallSites {
		if found[site] == 0 {
			t.Errorf("listed %q (%s) no longer builds an LLM client or decision evaluator; remove the entry", site, listed.reason)
		}
	}
}

var scanLLMCallSitesOnce = sync.OnceValues(func() (map[string]int, error) {
	return scanLLMCallSites(filepath.Join("..", ".."))
})

func scannedLLMCallSites(t *testing.T) map[string]int {
	t.Helper()
	sites, err := scanLLMCallSitesOnce()
	if err != nil {
		t.Fatal(err)
	}
	return sites
}

func scanLLMCallSites(repoRoot string) (map[string]int, error) {
	return scanFirstPartySource(repoRoot, constructorCallers)
}

func scanFirstPartySource(repoRoot string, count func(path, rel string) (map[string]int, error)) (map[string]int, error) {
	sites := map[string]int{}
	err := walkFirstPartySource(repoRoot, func(path, rel string) error {
		callers, scanErr := count(path, rel)
		if scanErr != nil {
			return scanErr
		}
		for fn, constructions := range callers {
			sites[rel+":"+fn] += constructions
		}
		return nil
	})
	return sites, err
}

func walkFirstPartySource(repoRoot string, visit func(path, rel string) error) error {
	for _, root := range []string{"cmd", "internal", "pkg", "tools"} {
		err := filepath.WalkDir(filepath.Join(repoRoot, root), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == ".claude" || d.Name() == "node_modules" || d.Name() == "vendor" || d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				return relErr
			}
			return visit(path, filepath.ToSlash(rel))
		})
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("walk %s: %w", root, err)
		}
	}
	return nil
}

func constructorCallers(path, rel string) (map[string]int, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	ownPackage := modulePath + "/" + filepath.ToSlash(filepath.Dir(rel))
	imported, err := importNames(file, rel, func(importPath string) bool {
		return llmCallConstructors[importPath] != nil
	})
	if err != nil {
		return nil, err
	}
	isConstructor := func(pkgPath, name string) bool {
		return slices.Contains(llmCallConstructors[pkgPath], name)
	}

	var countConstructors func(node ast.Node) int
	countConstructors = func(node ast.Node) int {
		count := 0
		ast.Inspect(node, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				if pkg, isIdent := n.X.(*ast.Ident); isIdent {
					if isConstructor(imported[pkg.Name], n.Sel.Name) {
						count++
					}
				} else {
					count += countConstructors(n.X)
				}
				return false
			case *ast.KeyValueExpr:
				if _, isField := n.Key.(*ast.Ident); isField {
					count += countConstructors(n.Value)
					return false
				}
			case *ast.Ident:
				if isConstructor(ownPackage, n.Name) {
					count++
				}
			}
			return true
		})
		return count
	}

	callers := map[string]int{}
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if decl.Body != nil {
				if constructions := countConstructors(decl.Body); constructions > 0 {
					callers[funcName(decl)] += constructions
				}
			}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				value, isValue := spec.(*ast.ValueSpec)
				if !isValue {
					continue
				}
				for i, v := range value.Values {
					if constructions := countConstructors(v); constructions > 0 {
						callers[value.Names[min(i, len(value.Names)-1)].Name] += constructions
					}
				}
			}
		}
	}
	return callers, nil
}

func importNames(file *ast.File, rel string, guarded func(importPath string) bool) (map[string]string, error) {
	imported := map[string]string{}
	for _, spec := range file.Imports {
		importPath, _ := strconv.Unquote(spec.Path.Value)
		name := importPath[strings.LastIndex(importPath, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == "." && guarded(importPath) {
			return nil, fmt.Errorf("%s dot-imports %s; import it by name so the guard can find its uses", rel, importPath)
		}
		imported[name] = importPath
	}
	return imported, nil
}

func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if generic, ok := recv.(*ast.IndexExpr); ok {
		recv = generic.X
	}
	if ident, ok := recv.(*ast.Ident); ok {
		return ident.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}
