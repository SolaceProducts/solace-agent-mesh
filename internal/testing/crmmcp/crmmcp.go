// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package crmmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	ToolFindCustomer = "find_customer"
	ToolListOrders   = "list_orders"
	ToolGetOrder     = "get_order"
)

type Config struct {
	Label string
}

type Server struct {
	URL string

	mu        sync.Mutex
	toolCalls []string
}

func Start(t testing.TB, cfg Config) *Server {
	t.Helper()
	s := &Server{}
	mcpSrv := server.NewMCPServer("acme-crm-fake", "1.0.0", server.WithToolCapabilities(true))
	for _, def := range toolDefs(cfg) {
		mcpSrv.AddTool(def.tool, s.record(def.tool.Name, def.run))
	}
	httpSrv := httptest.NewServer(server.NewStreamableHTTPServer(mcpSrv))
	t.Cleanup(httpSrv.Close)
	s.URL = httpSrv.URL + "/mcp"
	return s
}

func (s *Server) ToolCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.toolCalls...)
}

func (s *Server) record(name string, run func(args map[string]any) (any, error)) server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		s.mu.Lock()
		s.toolCalls = append(s.toolCalls, name)
		s.mu.Unlock()
		out, err := run(req.GetArguments())
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		body, marshalErr := json.Marshal(out)
		if marshalErr != nil {
			return nil, marshalErr
		}
		return mcp.NewToolResultStructured(out, string(body)), nil
	}
}

type toolDef struct {
	tool mcp.Tool
	run  func(args map[string]any) (any, error)
}

func toolDefs(cfg Config) []toolDef {
	return []toolDef{
		{
			tool: mcp.NewTool(ToolFindCustomer,
				mcp.WithDescription("Look a customer up by company name."),
				mcp.WithString("company", mcp.Required(), mcp.Description("Company name to search for."))),
			run: func(args map[string]any) (any, error) {
				company, _ := args["company"].(string)
				if company == "" {
					return nil, fmt.Errorf("company is required")
				}
				return map[string]any{
					"customerId": customerID(company),
					"company":    company,
					"tier":       "gold",
					"source":     cfg.Label,
				}, nil
			},
		},
		{
			tool: mcp.NewTool(ToolListOrders,
				mcp.WithDescription("List a customer's orders."),
				mcp.WithString("customerId", mcp.Required(), mcp.Description("Customer id from find_customer."))),
			run: func(args map[string]any) (any, error) {
				id, _ := args["customerId"].(string)
				if id == "" {
					return nil, fmt.Errorf("customerId is required")
				}
				return map[string]any{
					"customerId": id,
					"orders": []map[string]any{
						{"orderId": id + "-O1", "total": 120.5},
						{"orderId": id + "-O2", "total": 75},
					},
					"source": cfg.Label,
				}, nil
			},
		},
		{
			tool: mcp.NewTool(ToolGetOrder,
				mcp.WithDescription("Fetch one order."),
				mcp.WithString("orderId", mcp.Required(), mcp.Description("Order id."))),
			run: func(args map[string]any) (any, error) {
				id, _ := args["orderId"].(string)
				if id == "" {
					return nil, fmt.Errorf("orderId is required")
				}
				return map[string]any{"orderId": id, "status": "shipped", "source": cfg.Label}, nil
			},
		},
	}
}

func customerID(company string) string {
	return "C-" + strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(company), " ", "-"))
}
