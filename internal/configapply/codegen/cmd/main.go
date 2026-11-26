// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/codegen"
)

type stringSliceFlag []string

func (s *stringSliceFlag) String() string     { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	var (
		mode           = flag.String("mode", "translator", "generation mode: \"translator\" (default), \"schema-data\", or \"workflow-schema\"")
		pkg            = flag.String("package", "", "target package name (required for translator mode)")
		output         = flag.String("output", "", "output file path (required)")
		workflowConfig = flag.String("workflow-config", "", "path to internal/workflow/config.go (workflow-schema mode)")
		nullIsAbsent   = flag.Bool("null-is-absent", false, "reverse translators treat an empty or null value as no value, for specs generated from copies of /api/v2 types (translator mode)")
		specs          stringSliceFlag
		schemas        stringSliceFlag
	)
	flag.Var(&specs, "spec", "translation spec: Kind:Request:Response:DTOFile:ImportPath:ImportAlias (translator mode; may be repeated)")
	flag.Var(&schemas, "schema", "schema spec: kind=K:plural=P:request=R:file=F (schema-data mode; may be repeated)")
	flag.Parse()

	if *output == "" {
		flag.Usage()
		os.Exit(2)
	}

	switch *mode {
	case "translator":
		runTranslator(*pkg, *output, specs, *nullIsAbsent)
	case "schema-data":
		runSchemaData(*output, schemas)
	case "workflow-schema":
		runWorkflowSchema(*output, *workflowConfig)
	default:
		fail("unknown -mode %q (want \"translator\", \"schema-data\", or \"workflow-schema\")", *mode)
	}
}

func runWorkflowSchema(output, workflowConfig string) {
	if workflowConfig == "" {
		fail("workflow-schema mode requires -workflow-config <path-to-internal/workflow/config.go>")
	}
	wf, err := codegen.ExtractWorkflowSchema(workflowConfig)
	if err != nil {
		fail("extract workflow schema: %v", err)
	}
	source, err := codegen.EmitWorkflowSchema(wf)
	if err != nil {
		fail("emit workflow schema: %v", err)
	}
	if err := os.WriteFile(output, source, 0o644); err != nil {
		fail(writeErrFmt, output, err)
	}
}

func runTranslator(pkg, output string, specs stringSliceFlag, nullIsAbsent bool) {
	if pkg == "" || len(specs) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	parsed := make([]codegen.Spec, 0, len(specs))
	for _, raw := range specs {
		spec, err := parseSpec(raw)
		if err != nil {
			fail("invalid -spec %q: %v", raw, err)
		}
		spec.NullIsAbsent = nullIsAbsent
		parsed = append(parsed, spec)
	}
	source, err := codegen.Generate(pkg, parsed...)
	if err != nil {
		fail("generate: %v", err)
	}
	if err := os.WriteFile(output, source, 0o644); err != nil {
		fail(writeErrFmt, output, err)
	}
}

func runSchemaData(output string, raws stringSliceFlag) {
	if len(raws) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	parsed := make([]codegen.SchemaSpec, 0, len(raws))
	for _, raw := range raws {
		s, err := parseSchemaSpec(raw)
		if err != nil {
			fail("invalid -schema %q: %v", raw, err)
		}
		parsed = append(parsed, s)
	}
	source, err := codegen.EmitSchemaData(parsed)
	if err != nil {
		fail("emit schema-data: %v", err)
	}
	if err := os.WriteFile(output, source, 0o644); err != nil {
		fail(writeErrFmt, output, err)
	}
}

func parseSchemaSpec(raw string) (codegen.SchemaSpec, error) {
	pairs := strings.Split(raw, ":")
	kv := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" || v == "" {
			return codegen.SchemaSpec{}, fmt.Errorf("expected key=value, got %q", p)
		}
		kv[k] = v
	}
	for _, k := range []string{"kind", "plural", "request", "file"} {
		if kv[k] == "" {
			return codegen.SchemaSpec{}, fmt.Errorf("missing %s=", k)
		}
	}
	structs, err := codegen.ParseStructs(kv["file"], kv["request"])
	if err != nil {
		return codegen.SchemaSpec{}, fmt.Errorf("parse %s: %w", kv["file"], err)
	}
	return codegen.SchemaSpec{
		Kind:       kv["kind"],
		PluralPath: kv["plural"],
		Request:    structs[0],
		V2Copy:     kv["v2copy"] == "true",
	}, nil
}

func parseSpec(raw string) (codegen.Spec, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 6 {
		return codegen.Spec{}, fmt.Errorf("expected 6 colon-separated fields, got %d", len(parts))
	}
	kind, reqName, respName, file, importPath, alias := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]
	for i, name := range []string{"Kind", "Request", "Response", "DTOFile", "ImportPath", "ImportAlias"} {
		if parts[i] == "" {
			return codegen.Spec{}, fmt.Errorf("%s field is empty", name)
		}
	}

	structs, err := codegen.ParseStructsWithAlias(file, alias, reqName, respName)
	if err != nil {
		return codegen.Spec{}, fmt.Errorf("parse %s: %w", file, err)
	}
	return codegen.Spec{
		Kind:           kind,
		Request:        structs[0],
		Response:       structs[1],
		DTOImportPath:  importPath,
		DTOImportAlias: alias,
	}, nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "codegen: "+format+"\n", args...)
	os.Exit(1)
}

const (
	writeErrFmt = "write %s: %v"
)
