// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"fmt"
	"reflect"

	"github.com/swaggest/jsonschema-go"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/openapigen"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	usageMoneyPattern = `^-?\d+\.\d{10}$`
	usageMoneyExample = "0.0142000000"
)

func OpenAPIOperations() []openapigen.Operation {
	return operationsOf(specRoutes())
}

func OpenAPIErrorType() reflect.Type {
	return reflect.TypeFor[apicontract.ErrorBody]()
}

func OpenAPIAuthErrorType() reflect.Type {
	return reflect.TypeFor[entplatform.AuthError]()
}

func OpenAPIDocPackages() []string {
	return append(apicontract.DocPackages(),
		reflect.TypeFor[Agent]().PkgPath(),
		reflect.TypeFor[entplatform.AuthError]().PkgPath())
}

func OpenAPITagDescriptions() map[string]string {
	return tagDescriptionsOf(specRoutes())
}

func OpenAPITypeMapper(t reflect.Type) (*jsonschema.Schema, bool) {
	switch t {
	case restTimestampType:
		timestamp := new(jsonschema.Schema).WithType(jsonschema.String.Type()).WithFormat("date-time")
		timestamp.WithExtraPropertiesItem("example", apicontract.InstantExample)
		return timestamp, true
	case usageMoneyType:
		money := new(jsonschema.Schema).WithType(jsonschema.String.Type()).WithPattern(usageMoneyPattern)
		money.WithExtraPropertiesItem("example", usageMoneyExample)
		return money, true
	}
	return apicontract.TypeMapper(t)
}

func OpenAPIUnwrap(t reflect.Type) (reflect.Type, bool) { return apicontract.Unwrap(t) }

const specNamespace = "openapi-spec"

func specRoutes() []Route {
	deps, core := specDeps()
	return routeTable(deps, core, core.Logger())
}

func specDeps() (*entplatform.RouteDeps, *platpkg.PlatformCore) {
	deps := &entplatform.RouteDeps{}
	fillServices(reflect.ValueOf(deps).Elem())
	for _, g := range withheldRouteGroups {
		g.describe(deps)
	}
	core, err := platpkg.NewDescribingCore(map[string]any{"namespace": specNamespace})
	if err != nil {
		panic(fmt.Sprintf("describe the v2 routes: %v", err))
	}
	return deps, core
}

func fillServices(v reflect.Value) {
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if !f.CanSet() {
			continue
		}
		switch {
		case f.Kind() == reflect.Pointer && f.Type().Elem().Kind() == reflect.Struct:
			f.Set(reflect.New(f.Type().Elem()))
		case f.Kind() == reflect.Map:
			f.Set(reflect.MakeMap(f.Type()))
		case f.Type() == reflect.TypeFor[entplatform.ModelDependentsService]():
			f.Set(reflect.ValueOf(&entplatform.NoOpModelDependentsHandler{}))
		}
	}
}

func operationsOf(routes []Route) []openapigen.Operation {
	return apicontract.OperationsOf(routes, platpkg.APIV2Prefix, accessOf)
}

func operationOf(rt Route) openapigen.Operation {
	op := apicontract.OperationOf(rt, platpkg.APIV2Prefix, accessOf(rt))
	op.Description = descriptionOf(rt)
	return op
}

func OpenAPITagGroups() map[string]string {
	return apicontract.TagGroupsOf(specRoutes())
}

func tagDescriptionsOf(routes []Route) map[string]string {
	return apicontract.TagDescriptionsOf(routes)
}

const unofferedScopeNote = "The role editor does not offer `%s`. The built-in `sam_manager` role and the universal grant `*:*:*` hold it, and a role created through the API or `sam config` can include it."

func descriptionOf(rt Route) string {
	for _, scope := range rt.Scopes() {
		if rbac.IsUnassignableScope(scope) {
			return rt.Doc.Text + " " + fmt.Sprintf(unofferedScopeNote, scope)
		}
	}
	return rt.Doc.Text
}

func accessOf(rt Route) openapigen.Access {
	if rt.Gate != "" {
		doc, ok := gateDocs[rt.Gate]
		if !ok {
			panic(fmt.Sprintf("%s %s: gate %q has no entry in gateDocs", rt.Method, rt.Path, rt.Gate))
		}
		return openapigen.Access{Gate: doc.Text, APIKeys: rt.AllowAPIKey && doc.AdmitsAPIKeys, Public: rt.Gate == GatePublic}
	}
	return openapigen.Access{Scopes: rt.Scopes(), APIKeys: rt.AllowAPIKey}
}

var (
	restTimestampType = reflect.TypeFor[entplatform.RESTTimestamp]()
	usageMoneyType    = reflect.TypeFor[entplatform.UsageMoney]()
)
