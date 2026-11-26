// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/openapigen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

const (
	specTitle       = "Solace Agent Mesh Management API"
	specDescription = "REST API for managing agents, connectors, entrypoints, models, and deployments in Solace Agent Mesh."
	contactName     = "Solace"
	relativeServer  = "/"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gen-openapi:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("gen-openapi", flag.ContinueOnError)
	out := fs.String("out", "", "directory to write the spec files into")
	version := fs.String("version", "2.0.0", "value for info.version")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("-out is required")
	}

	chatRoutes := chatv2.Routes(chatv2.Deps{})
	docPackages := append(apiv2.OpenAPIDocPackages(), chatv2.DocPackage())
	docs, err := openapigen.NewASTDocsForPackages(docPackages...)
	if err != nil {
		return fmt.Errorf("load doc comments: %w", err)
	}
	operations := append(apiv2.OpenAPIOperations(), chatOperations(chatRoutes)...)
	tagDescriptions, err := merged(apiv2.OpenAPITagDescriptions(), chatv2.TagDescriptions())
	if err != nil {
		return err
	}
	tagGroups, err := merged(apiv2.OpenAPITagGroups(), chatv2.TagGroups())
	if err != nil {
		return err
	}
	specs, err := openapigen.Build(operations, openapigen.Options{
		Title:           specTitle,
		Description:     specDescription,
		Version:         *version,
		ContactName:     contactName,
		ServerURLs:      []string{relativeServer},
		Docs:            docs,
		Mapper:          apiv2.OpenAPITypeMapper,
		Unwrap:          apiv2.OpenAPIUnwrap,
		TagDescriptions: tagDescriptions,
		TagGroups:       tagGroups,
		ErrorType:       apiv2.OpenAPIErrorType(),
		AuthErrorType:   apiv2.OpenAPIAuthErrorType(),
	})
	if err != nil {
		return fmt.Errorf("build specs:\n%w", err)
	}
	return openapigen.WriteSpecs(*out, specs)
}

func chatOperations(routes []apicontract.Route) []openapigen.Operation {
	return apicontract.OperationsOf(routes, chatv2.Prefix, chatv2.Access)
}

func merged(sources ...map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, m := range sources {
		for k, v := range m {
			if _, dup := out[k]; dup {
				return nil, fmt.Errorf("tag %q is defined by more than one API", k)
			}
			out[k] = v
		}
	}
	return out, nil
}
