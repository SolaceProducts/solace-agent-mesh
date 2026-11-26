// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestEveryConstructorDeclaresItsTrustFlags(t *testing.T) {
	ctx := context.Background()
	must := func(s *Subject, err error) *Subject {
		t.Helper()
		if err != nil {
			t.Fatalf("constructor: %v", err)
		}
		return s
	}
	slackUser := ForSlackUser("alice@corp.com", "alice@corp.com", "U1", "T1")
	sender := ForWhatsAppSenderIdentity("15550005555", "Alice")

	cases := map[string]struct {
		build        func() *Subject
		fullIdentity bool
	}{
		"DevIdentity":        {DevIdentity, true},
		"SystemUserIdentity": {func() *Subject { return SystemUserIdentity("ops", AuthMethodWebhook) }, true},
		"ForSAMAccessToken": {func() *Subject {
			return must(ForSAMAccessToken("tok", &fakeTokens{claims: TokenClaims{UserID: "u1"}}, nil))
		}, true},
		"ForIDPAccessToken": {func() *Subject {
			return must(ForIDPAccessToken(ctx, ExternalID{Type: IDSub, Value: "u1"}, "", "", nil, &fakeLookup{}))
		}, true},
		"ForPlatformAdminToken": {func() *Subject { return ForPlatformAdminToken(TokenClaims{UserID: "admin"}, nil) }, true},
		"ForScheduledTaskOwner": {func() *Subject {
			return must(ForScheduledTaskOwner(ctx, "u1", &fakeLookup{ent: Entitlement{Scopes: []string{"s"}}}))
		}, true},
		"ForScheduledRunAs": {func() *Subject {
			return must(ForScheduledRunAs(ctx, "system:default", &fakeLookup{ent: Entitlement{Scopes: []string{"s"}}}))
		}, true},
		"ForEvalSystemPrincipal": {func() *Subject {
			return must(ForEvalSystemPrincipal("system:eval", "", rbac.Resolution{Scopes: []string{"s"}}))
		}, true},
		"ForSyntheticMonitor":                {func() *Subject { return ForSyntheticMonitor(nil, nil, nil) }, true},
		"ForSentinelMonitor":                 {func() *Subject { return ForSentinelMonitor("sentinel-monitor", []string{"s"}) }, true},
		"ForMcpDefaultCaller":                {func() *Subject { return ForMcpDefaultCaller(nil, nil, false) }, true},
		"ForNoopDevCaller":                   {func() *Subject { return ForNoopDevCaller("", DevHeaders{}, nil) }, true},
		"ForDevMiddlewareCaller":             {func() *Subject { return ForDevMiddlewareCaller(nil) }, true},
		"ForWebhookCaller":                   {func() *Subject { return ForWebhookCaller("ops") }, true},
		"ForEventMeshCaller":                 {func() *Subject { return must(ForEventMeshCaller(&ExternalID{Type: IDSub, Value: "u1"}, "ops")) }, true},
		"ForEventMeshCaller, no external id": {func() *Subject { return must(ForEventMeshCaller(nil, "ops")) }, true},
		"ForEmailSender":                     {func() *Subject { return must(ForEmailSender(ExternalID{Type: IDEmail, Value: "a@b.com"}, "", false)) }, true},
		"ForEmailHILResponder":               {func() *Subject { return ForEmailHILResponder("a@b.com") }, true},
		"ForSlackUser":                       {func() *Subject { return slackUser }, true},
		"ForTeamsUser":                       {func() *Subject { return ForTeamsUser("bob@corp.com", "bob@corp.com", "Bob", "T1", "aad-1", "graph") }, true},
		"ForChatUser":                        {func() *Subject { id, _ := ForChatUser(ctx, slackUser, nil, ChannelPolicy{}); return id }, true},
		"ForChatResponder":                   {func() *Subject { return ForChatResponder(slackUser, ChannelPolicy{}) }, true},
		"ForWhatsAppSenderIdentity":          {func() *Subject { return sender }, true},
		"ForWhatsAppSender":                  {func() *Subject { id, _ := ForWhatsAppSender(sender, ""); return id }, true},
		"ForAuthorizedWhatsAppSender":        {func() *Subject { return ForAuthorizedWhatsAppSender(sender, sender.userID, SenderAuthorization{}) }, true},
		"ForAuthorizedWhatsAppSender, from a user id only": {func() *Subject {
			return ForAuthorizedWhatsAppSender(ForUserIDOnly("whatsapp:15550005555"), "alice@corp.com", SenderAuthorization{})
		}, true},
		"ForGoogleChatSender": {func() *Subject { return ForGoogleChatSender("a@b.com", "A", "users/1", SenderAuthorization{}) }, true},
		"ForGoogleChatUser": {func() *Subject {
			id, _ := ForGoogleChatUser(ForGoogleChatSender("a@b.com", "A", "users/1", SenderAuthorization{}), ChannelPolicy{IsMultiParty: true})
			return id
		}, true},
		"ForGoogleChatResponder": {func() *Subject {
			return ForGoogleChatResponder(ForGoogleChatSender("a@b.com", "A", "users/1", SenderAuthorization{}), true, "ops")
		}, true},
		"ForAPIKey": {func() *Subject { return ForAPIKey(apikey.Resolution{OwnerSub: "system:ci"}) }, true},
		"ResolveAPIKey": {func() *Subject {
			id, _ := ResolveAPIKey(ctx, &fakeKeyAuthority{resolution: apikey.Resolution{OwnerSub: "system:ci"}}, testKeyPlaintext)
			return id
		}, true},
		"ResolveRunAsIdentity": {func() *Subject {
			id, _ := ResolveRunAsIdentity(RunAsRequest{Real: slackUser, IsMultiParty: true, RunAs: "ops"})
			return id
		}, true},

		"ForUserIDOnly": {func() *Subject { return ForUserIDOnly("u1") }, false},
	}

	resolvesScopes := map[string]bool{
		"ForAuthorizedWhatsAppSender":                      true,
		"ForAuthorizedWhatsAppSender, from a user id only": true,
		"ForGoogleChatSender":                              true,
		"ForAPIKey":                                        true,
		"ResolveAPIKey":                                    true,
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := tc.build()
			if got := s.FullIdentity(); got != tc.fullIdentity {
				t.Errorf("FullIdentity() = %v, want %v", got, tc.fullIdentity)
			}
			if got := s.ScopesResolved(); got != resolvesScopes[name] {
				t.Errorf("ScopesResolved() = %v, want %v", got, resolvesScopes[name])
			}
		})
	}

	readers := map[string]bool{"SubjectFromContext": true}
	for _, name := range exportedCallerConstructors(t) {
		if _, ok := cases[name]; !ok && !readers[name] {
			t.Errorf("%s builds a Subject but is not listed here: decide its full-identity and scopes-resolved flags", name)
		}
	}
}

func exportedCallerConstructors(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var names []string
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() || fn.Type.Results == nil {
				continue
			}
			res := fn.Type.Results.List[0].Type
			if star, ok := res.(*ast.StarExpr); ok {
				if id, ok := star.X.(*ast.Ident); ok && id.Name == "Subject" && fn.Name.Name != "NewTestSubject" {
					names = append(names, fn.Name.Name)
				}
			}
		}
	}
	slices.Sort(names)
	return names
}
