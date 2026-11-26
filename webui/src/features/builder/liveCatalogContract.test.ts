// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test, vi } from "vitest";

import { toolsetsApi } from "@/api/toolsets/versioned";

import type { Toolset } from "@/types";

import live from "./__fixtures__/live_toolsets.json";
import { extractAgentMetadata, fetchBuiltinToolsetLookup, resolveToolsetBindings } from "./builderUtils";

function stubCatalog() {
    vi.spyOn(toolsetsApi, "getBuiltin").mockResolvedValue(live.data as unknown as Toolset[]);
}

describe("builder translation against a live toolset catalog", () => {
    test("the catalog carries the tool names and aliases the translation needs", () => {
        const web = live.data.find((t: { id: string }) => t.id === "builtin_web_request_tools");
        expect(web).toBeDefined();
        expect(web?.tools.map((x: { toolName: string }) => x.toolName)).toEqual(["web_request", "web_search_google"]);
        expect(web?.aliases).toContain("web_tools");
    });

    test("a Quick Build agent asking for web_request binds the toolset and excludes the sibling", async () => {
        stubCatalog();
        const meta = extractAgentMetadata(`
apps:
  - name: DocsFetcher
    app_config:
      agent_name: DocsFetcher
      instruction: |
        ${"x".repeat(150)}
      agent_card:
        description: Fetches and summarizes pages from docs.solace.com.
      tools:
        - tool_type: builtin
          tool_name: web_request
`);

        const bindings = resolveToolsetBindings(meta?.builtinGroupNames ?? [], meta?.builtinToolNames ?? [], await fetchBuiltinToolsetLookup());

        expect(bindings.toolsets).toEqual(["builtin_web_request_tools"]);
        expect(bindings.toolsetExcludedTools).toEqual({ builtin_web_request_tools: ["web_search_google"] });
        vi.restoreAllMocks();
    });

    test("the alias spelling resolves to the same platform id the server stores", async () => {
        stubCatalog();
        const lookup = await fetchBuiltinToolsetLookup();

        expect(resolveToolsetBindings(["web_tools"], [], lookup).toolsets).toEqual(["builtin_web_request_tools"]);
        vi.restoreAllMocks();
    });

    test("web_search_google's two real owners are both recorded, so a group grant covers it", async () => {
        stubCatalog();
        const lookup = await fetchBuiltinToolsetLookup();

        expect(lookup.toolsetIdsByToolName.get("web_search_google")).toEqual(expect.arrayContaining(["builtin_web_request_tools", "builtin_research_tools"]));
        expect(resolveToolsetBindings(["web_tools"], ["web_search_google"], lookup).toolsets).toEqual(["builtin_web_request_tools"]);
        vi.restoreAllMocks();
    });

    test("a tool no catalogued toolset provides fails the save instead of vanishing", async () => {
        stubCatalog();
        const lookup = await fetchBuiltinToolsetLookup();

        expect(() => resolveToolsetBindings([], ["schedule_task"], lookup)).toThrow(/schedule_task/);
        vi.restoreAllMocks();
    });
});
