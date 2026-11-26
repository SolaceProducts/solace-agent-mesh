// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { CreateRemoteAgentRequest } from "@/api/remote-agents";

import type { BuildContext, ComponentCanvasData, DependencyProgress, ManifestComponent } from "./useBuilderCanvasData";
import {
    arrEq,
    buildCandidateEntry,
    computeDependencyProgress,
    depProgressEq,
    depProgressStatus,
    eligibleForDepProgress,
    entryEq,
    isComponentQueued,
    isComponentWaitingForInput,
    parseTypeMetadata,
    parseWorkflowNodeSummary,
} from "./useBuilderCanvasData";
import type { BuildManifest } from "./useBuilderManifest";

function ctx(overrides: Partial<BuildContext> = {}): BuildContext {
    return {
        resolvedConfigs: new Map(),
        loadingSet: new Set(),
        generatingFilenames: new Set(),
        isResponding: false,
        buildFinalizing: false,
        hasAnyGenerating: false,
        changedSinceValidation: new Set(),
        pendingInputComponentName: null,
        createdNames: new Set(),
        a2aFallbackByName: new Map(),
        ...overrides,
    };
}

function entry(overrides: Partial<ComponentCanvasData> = {}): ComponentCanvasData {
    return {
        name: "x",
        filename: "x.yaml",
        type: "agent",
        yamlText: null,
        isLoading: false,
        isGenerating: false,
        isQueued: false,
        isWaitingForInput: false,
        isBuildingDeps: false,
        isBuilderResponding: false,
        dependencyProgress: [],
        ...overrides,
    };
}

function comp(overrides: Partial<ManifestComponent> = {}): ManifestComponent {
    return { name: "x", filename: "x.yaml", type: "agent", ...overrides } as ManifestComponent;
}

describe("arrEq", () => {
    test("returns true for the same reference", () => {
        const a = [1, 2, 3];
        expect(arrEq(a, a)).toBe(true);
    });
    test("returns true for equal arrays", () => {
        expect(arrEq([1, 2, 3], [1, 2, 3])).toBe(true);
    });
    test("returns false when one side is undefined", () => {
        expect(arrEq(undefined, [1])).toBe(false);
        expect(arrEq([1], undefined)).toBe(false);
    });
    test("returns true when both are undefined", () => {
        expect(arrEq(undefined, undefined)).toBe(true);
    });
    test("returns false on length mismatch", () => {
        expect(arrEq([1, 2], [1, 2, 3])).toBe(false);
    });
    test("returns false on element mismatch", () => {
        expect(arrEq([1, 2, 3], [1, 9, 3])).toBe(false);
    });
});

describe("depProgressEq", () => {
    const a: DependencyProgress = { name: "a", type: "connector", status: "queued" };
    const b: DependencyProgress = { name: "b", type: "connector", status: "queued" };
    test("returns true for identical arrays", () => {
        expect(depProgressEq([a, b], [a, b])).toBe(true);
    });
    test("returns false on length mismatch", () => {
        expect(depProgressEq([a], [a, b])).toBe(false);
    });
    test("returns false on name mismatch", () => {
        expect(depProgressEq([a], [{ ...a, name: "z" }])).toBe(false);
    });
    test("returns false on status mismatch", () => {
        expect(depProgressEq([a], [{ ...a, status: "generating" }])).toBe(false);
    });
    test("returns false on icon mismatch", () => {
        expect(depProgressEq([a], [{ ...a, icon: "jira" }])).toBe(false);
    });
});

describe("entryEq", () => {
    test("returns true for entries with identical fields", () => {
        const e1 = entry({ yamlText: "x", isLoading: true });
        const e2 = entry({ yamlText: "x", isLoading: true });
        expect(entryEq(e1, e2)).toBe(true);
    });
    test("returns false when yamlText differs", () => {
        expect(entryEq(entry({ yamlText: "a" }), entry({ yamlText: "b" }))).toBe(false);
    });
    test("returns false when dependsOn arrays differ", () => {
        expect(entryEq(entry({ dependsOn: ["a"] }), entry({ dependsOn: ["b"] }))).toBe(false);
    });
    test("returns false when a parsed-config reference differs", () => {
        const cfg1 = { name: "agent" } as ComponentCanvasData["agentConfig"];
        const cfg2 = { name: "agent" } as ComponentCanvasData["agentConfig"];
        expect(entryEq(entry({ agentConfig: cfg1 }), entry({ agentConfig: cfg2 }))).toBe(false);
    });
    test("compares skillConfig — a differing reference is not equal", () => {
        const s1 = { references: ["a.md"], tools: [] };
        const s2 = { references: ["a.md"], tools: [] };
        expect(entryEq(entry({ skillConfig: s1 }), entry({ skillConfig: s2 }))).toBe(false);
        expect(entryEq(entry({ skillConfig: s1 }), entry({ skillConfig: s1 }))).toBe(true);
    });
    test("a recorded id busts the cache (so a deploy that records the id re-derives the live overlay)", () => {
        expect(entryEq(entry({ id: undefined }), entry({ id: "01a160d0-2474-7426-830a-ae0397fcdc9a" }))).toBe(false);
        expect(entryEq(entry({ id: "a" }), entry({ id: "a" }))).toBe(true);
    });
    test("an isBuildFinalizing flip busts the cache (so the card leaves 'Creating' when the turn settles)", () => {
        expect(entryEq(entry({ isBuildFinalizing: true }), entry({ isBuildFinalizing: false }))).toBe(false);
        expect(entryEq(entry({ isBuildFinalizing: false }), entry({ isBuildFinalizing: undefined }))).toBe(true);
    });
    test("an isBuilderResponding flip busts the cache (so the start card stops reading a stopped build as live)", () => {
        expect(entryEq(entry({ isBuilderResponding: true }), entry({ isBuilderResponding: false }))).toBe(false);
        expect(entryEq(entry({ isBuilderResponding: false }), entry({ isBuilderResponding: false }))).toBe(true);
    });
});

describe("isComponentQueued", () => {
    test("returns false when isResponding is false", () => {
        const c = comp();
        expect(isComponentQueued(c, null, false, ctx({ isResponding: false, hasAnyGenerating: true }))).toBe(false);
    });
    test("returns false when nothing is generating", () => {
        const c = comp();
        expect(isComponentQueued(c, null, false, ctx({ isResponding: true, hasAnyGenerating: false }))).toBe(false);
    });
    test("returns false when this component is generating", () => {
        const c = comp();
        expect(isComponentQueued(c, null, true, ctx({ isResponding: true, hasAnyGenerating: true }))).toBe(false);
    });
    test("returns false when status is terminal", () => {
        for (const status of ["validated", "deployed", "error"] as const) {
            expect(isComponentQueued(comp({ status }), null, false, ctx({ isResponding: true, hasAnyGenerating: true }))).toBe(false);
        }
    });
    test("returns false for platform-origin components", () => {
        expect(isComponentQueued(comp({ origin: "platform" }), null, false, ctx({ isResponding: true, hasAnyGenerating: true }))).toBe(false);
    });
    test("returns false when yamlText is already populated", () => {
        expect(isComponentQueued(comp(), "name: x", false, ctx({ isResponding: true, hasAnyGenerating: true }))).toBe(false);
    });
    test("returns false when the component has ever been created", () => {
        const created = new Set(["x"]);
        expect(isComponentQueued(comp(), null, false, ctx({ isResponding: true, hasAnyGenerating: true, createdNames: created }))).toBe(false);
    });
    test("returns true when all guards pass", () => {
        expect(isComponentQueued(comp(), null, false, ctx({ isResponding: true, hasAnyGenerating: true }))).toBe(true);
    });
});

describe("isComponentWaitingForInput", () => {
    test("returns false when no pending name", () => {
        expect(isComponentWaitingForInput(comp({ name: "x" }), null)).toBe(false);
    });
    test("returns true on exact name match", () => {
        expect(isComponentWaitingForInput(comp({ name: "JiraConn" }), "JiraConn")).toBe(true);
    });
    test("returns true on case-insensitive name match", () => {
        expect(isComponentWaitingForInput(comp({ name: "JiraConn" }), "jiraconn")).toBe(true);
    });
    test("returns false on different names", () => {
        expect(isComponentWaitingForInput(comp({ name: "JiraConn" }), "SlackConn")).toBe(false);
    });
    test("a blocked session component waits on the user with no open question", () => {
        expect(isComponentWaitingForInput(comp({ name: "inventory_api", status: "blocked", origin: "session" }), null)).toBe(true);
    });
    test("a blocked platform component does not", () => {
        expect(isComponentWaitingForInput(comp({ name: "inventory_api", status: "blocked", origin: "platform" }), null)).toBe(false);
    });
});

describe("parseWorkflowNodeSummary", () => {
    test("returns null on parse failure", () => {
        expect(parseWorkflowNodeSummary("[[[ not yaml")).toBeNull();
    });
    test("returns null when nodes are missing", () => {
        expect(parseWorkflowNodeSummary("apps: []")).toBeNull();
    });
    test("counts node types and lists agent names", () => {
        const yaml = `apps:
  - app_config:
      nodes:
        - type: agent
          agent_name: A1
        - type: agent
          agent_name: A2
        - type: switch
`;
        const summary = parseWorkflowNodeSummary(yaml);
        expect(summary).toEqual({
            total: 3,
            agents: ["A1", "A2"],
            types: { agent: 2, switch: 1 },
        });
    });
    test("falls back to 'unknown' for nodes without a type field", () => {
        const yaml = `apps:
  - app_config:
      nodes:
        - {}
`;
        const summary = parseWorkflowNodeSummary(yaml);
        expect(summary?.types).toEqual({ unknown: 1 });
    });
    test("counts nodes nested under app_config.workflow (config-apply-authored shape)", () => {
        const yaml = `apps:
  - app_config:
      tools: []
      workflow:
        nodes:
          - type: agent
            agent_name: A1
          - type: switch
`;
        const summary = parseWorkflowNodeSummary(yaml);
        expect(summary).toEqual({
            total: 2,
            agents: ["A1"],
            types: { agent: 1, switch: 1 },
        });
    });
});

describe("parseTypeMetadata", () => {
    test("returns the agent config for agent yaml", () => {
        const out = parseTypeMetadata("agent", "apps:\n  - app_config:\n      agent_name: A\n");
        expect(out.agentConfig).toBeDefined();
    });
    test("returns gateway metadata for gateway yaml", () => {
        const out = parseTypeMetadata("gateway", "apps:\n  - app_config:\n      type: webhook\n");
        expect(out).toHaveProperty("gatewayMetadata");
    });
    test("returns connector metadata for connector yaml", () => {
        const out = parseTypeMetadata("connector", "name: c\ntype: db\n");
        expect(out).toHaveProperty("connectorMetadata");
    });
    test("returns skillConfig (references + tools) for skill yaml", () => {
        const out = parseTypeMetadata("skill", "name: s\ndescription: d\nreferences:\n  - name: r.md\ntools:\n  - web_request\n");
        expect(out.skillConfig).toEqual({ references: ["r.md"], tools: ["web_request"] });
    });
    test("returns a2aAgentMetadata for a2a_agent json", () => {
        const out = parseTypeMetadata("a2a_agent", JSON.stringify({ name: "Proxy", agentUrl: "http://h:9000", agentCardLocation: "well_known", agentAuthConfig: { type: "none" } }));
        expect(out).toHaveProperty("a2aAgentMetadata");
        expect((out.a2aAgentMetadata as { agentUrl?: string } | null)?.agentUrl).toBe("http://h:9000");
    });
    test("returns workflowConfig (no summary) for workflow yaml without parseable nodes", () => {
        const out = parseTypeMetadata("workflow", "name: w\n");
        expect(out).toHaveProperty("workflowConfig");
        expect(out.workflowNodeSummary).toBeUndefined();
    });
    test("includes workflowNodeSummary when nodes parse", () => {
        const yaml = "apps:\n  - app_config:\n      nodes:\n        - type: agent\n          agent_name: A\n";
        const out = parseTypeMetadata("workflow", yaml);
        expect(out.workflowNodeSummary).toMatchObject({ total: 1, agents: ["A"] });
    });
    test("returns an empty object for the unknown type", () => {
        expect(parseTypeMetadata("unknown", "x")).toEqual({});
    });
});

describe("depProgressStatus", () => {
    test("returns 'waiting' when waiting for input", () => {
        expect(depProgressStatus(entry({ isWaitingForInput: true }))).toBe("waiting");
    });
    test("returns 'generating' when generating", () => {
        expect(depProgressStatus(entry({ isGenerating: true }))).toBe("generating");
    });
    test("returns 'done' when terminal", () => {
        expect(depProgressStatus(entry({ status: "validated" }))).toBe("done");
        expect(depProgressStatus(entry({ status: "deployed" }))).toBe("done");
    });
    test("returns 'queued' otherwise", () => {
        expect(depProgressStatus(entry())).toBe("queued");
    });
});

describe("eligibleForDepProgress", () => {
    test("true for queued agents and workflows", () => {
        expect(eligibleForDepProgress(entry({ isQueued: true, type: "agent" }))).toBe(true);
        expect(eligibleForDepProgress(entry({ isQueued: true, type: "workflow" }))).toBe(true);
    });
    test("false for non-queued entries", () => {
        expect(eligibleForDepProgress(entry({ isQueued: false, type: "agent" }))).toBe(false);
    });
    test("false for connectors and gateways even if queued", () => {
        expect(eligibleForDepProgress(entry({ isQueued: true, type: "connector" }))).toBe(false);
        expect(eligibleForDepProgress(entry({ isQueued: true, type: "gateway" }))).toBe(false);
    });
});

describe("computeDependencyProgress", () => {
    const manifest: BuildManifest = {
        name: "m",
        components: [
            { name: "A", filename: "a.yaml", type: "agent", depends_on: ["Conn", "Skill"] } as ManifestComponent,
            { name: "Conn", filename: "c.yaml", type: "connector", icon: "jira" } as ManifestComponent,
            { name: "Skill", filename: "s.yaml", type: "connector" } as ManifestComponent,
        ],
    };

    test("returns [] when component has no deps", () => {
        const m: BuildManifest = { name: "m", components: [{ name: "A", filename: "a.yaml", type: "agent" } as ManifestComponent] };
        const result = new Map([["A", entry({ name: "A", type: "agent" })]]);
        expect(computeDependencyProgress(m.components![0], result, m)).toEqual([]);
    });

    test("skips platform-origin deps", () => {
        const result = new Map([
            ["A", entry({ name: "A", type: "agent" })],
            ["Conn", entry({ name: "Conn", type: "connector", origin: "platform" })],
            ["Skill", entry({ name: "Skill", type: "connector", isGenerating: true })],
        ]);
        const deps = computeDependencyProgress(manifest.components![0], result, manifest);
        expect(deps).toHaveLength(1);
        expect(deps[0].name).toBe("Skill");
    });

    test("carries the dep's icon from the manifest", () => {
        const result = new Map([
            ["A", entry({ name: "A", type: "agent" })],
            ["Conn", entry({ name: "Conn", type: "connector", isGenerating: true })],
            ["Skill", entry({ name: "Skill", type: "connector" })],
        ]);
        const deps = computeDependencyProgress(manifest.components![0], result, manifest);
        const conn = deps.find(d => d.name === "Conn");
        expect(conn?.icon).toBe("jira");
        expect(conn?.status).toBe("generating");
    });
});

describe("buildCandidateEntry a2a fallback", () => {
    const fallback: CreateRemoteAgentRequest = {
        agentUrl: "https://example.test/mult/",
        name: "MultiplicationAgent",
        agentCardLocation: "none",
        agentAuthConfig: { type: "bearer", token: "x" },
    };

    test("fills a2aAgentMetadata from the platform lookup when the component has no config artifact", () => {
        const entry = buildCandidateEntry(comp({ name: "MultiplicationAgent", filename: "", type: "a2a_agent", origin: "platform" }), ctx({ a2aFallbackByName: new Map([["MultiplicationAgent", fallback]]) }), undefined);
        expect(entry.a2aAgentMetadata).toBe(fallback);
        expect(entry.a2aAgentMetadata?.agentAuthConfig?.type).toBe("bearer");
    });

    test("does not override metadata parsed from a config artifact", () => {
        const yaml = JSON.stringify({ name: "MultiplicationAgent", agentUrl: "https://a/", agentCardLocation: "none", agentAuthConfig: { type: "apikey", headerName: "X", apiKey: "k" } });
        const entry = buildCandidateEntry(
            comp({ name: "MultiplicationAgent", filename: "m.json", type: "a2a_agent" }),
            ctx({ resolvedConfigs: new Map([["m.json", yaml]]), a2aFallbackByName: new Map([["MultiplicationAgent", fallback]]) }),
            undefined
        );
        expect(entry.a2aAgentMetadata?.agentAuthConfig?.type).toBe("apikey");
    });

    test("leaves metadata null when no artifact and no lookup entry", () => {
        const entry = buildCandidateEntry(comp({ name: "MultiplicationAgent", filename: "", type: "a2a_agent", origin: "platform" }), ctx(), undefined);
        expect(entry.a2aAgentMetadata ?? null).toBeNull();
    });

    test("resolves the lookup across casing/punctuation drift via the normalized name", () => {
        const entry = buildCandidateEntry(comp({ name: "multiplication_agent", filename: "", type: "a2a_agent", origin: "platform" }), ctx({ a2aFallbackByName: new Map([["multiplicationagent", fallback]]) }), undefined);
        expect(entry.a2aAgentMetadata).toBe(fallback);
    });

    test("does not apply the fallback when a config artifact is present but unparseable", () => {
        const entry = buildCandidateEntry(
            comp({ name: "MultiplicationAgent", filename: "m.json", type: "a2a_agent" }),
            ctx({ resolvedConfigs: new Map([["m.json", "{ not valid json"]]), a2aFallbackByName: new Map([["MultiplicationAgent", fallback]]) }),
            undefined
        );
        expect(entry.a2aAgentMetadata ?? null).toBeNull();
    });
});

describe("buildCandidateEntry id", () => {
    test("carries the id the session recorded for the component, so the live overlay can use it", () => {
        const withId = buildCandidateEntry(comp({ name: "A", status: "validated", origin: "session", id: "01a160d0-2474-7426-830a-ae0397fcdc9a" }), ctx({}), undefined);
        expect(withId.id).toBe("01a160d0-2474-7426-830a-ae0397fcdc9a");
        expect(buildCandidateEntry(comp({ name: "A", status: "validated", origin: "session" }), ctx({}), undefined).id).toBeUndefined();
    });
});

describe("buildCandidateEntry changedSinceValidation", () => {
    test("a component whose file changed since the stamp is marked", () => {
        const entry = buildCandidateEntry(comp({ name: "A", filename: "a.yaml", status: "validated", origin: "session" }), ctx({ changedSinceValidation: new Set(["a.yaml"]) }), undefined);
        expect(entry.changedSinceValidation).toBe(true);
    });

    test("a component whose file did not change is not", () => {
        const entry = buildCandidateEntry(comp({ name: "B", filename: "b.yaml", status: "validated", origin: "session" }), ctx({ changedSinceValidation: new Set(["a.yaml"]) }), undefined);
        expect(entry.changedSinceValidation).toBe(false);
    });

    test("a cached entry is not reused once the mark flips", () => {
        expect(entryEq(entry({ changedSinceValidation: true }), entry({ changedSinceValidation: false }))).toBe(false);
        expect(entryEq(entry({}), entry({ changedSinceValidation: false }))).toBe(true);
    });
});

describe("buildCandidateEntry isBuildFinalizing", () => {
    test("a validated session component is finalizing while the build turn is (buildFinalizing)", () => {
        const entry = buildCandidateEntry(comp({ name: "A", status: "validated", origin: "session" }), ctx({ buildFinalizing: true }), undefined);
        expect(entry.isBuildFinalizing).toBe(true);
    });

    test("not finalizing once the turn has settled (buildFinalizing false)", () => {
        const entry = buildCandidateEntry(comp({ name: "A", status: "validated", origin: "session" }), ctx({ buildFinalizing: false }), undefined);
        expect(entry.isBuildFinalizing).toBe(false);
    });

    test("a draft component that has NOT generated this turn is not finalizing (plan review)", () => {
        const entry = buildCandidateEntry(comp({ name: "A", status: "draft", origin: "session" }), ctx({ buildFinalizing: true }), undefined);
        expect(entry.isBuildFinalizing).toBe(false);
    });

    test("a draft component that HAS generated this turn is finalizing (the gap fix)", () => {
        const entry = buildCandidateEntry(comp({ name: "A", status: "draft", origin: "session" }), ctx({ buildFinalizing: true, createdNames: new Set(["A"]) }), undefined);
        expect(entry.isBuildFinalizing).toBe(true);
    });

    test("a created-this-turn draft is NOT finalizing once the turn settles (buildFinalizing false)", () => {
        const entry = buildCandidateEntry(comp({ name: "A", status: "draft", origin: "session" }), ctx({ buildFinalizing: false, createdNames: new Set(["A"]) }), undefined);
        expect(entry.isBuildFinalizing).toBe(false);
    });

    test("a deployed component is never finalizing", () => {
        const entry = buildCandidateEntry(comp({ name: "A", status: "deployed", origin: "session" }), ctx({ buildFinalizing: true }), undefined);
        expect(entry.isBuildFinalizing).toBe(false);
    });

    test("a platform-origin component is never finalizing (it owns its own lifecycle)", () => {
        const entry = buildCandidateEntry(comp({ name: "A", status: "validated", origin: "platform" }), ctx({ buildFinalizing: true }), undefined);
        expect(entry.isBuildFinalizing).toBe(false);
    });
});

describe.each([true, false])("buildCandidateEntry with the builder responding: %s", responding => {
    test.each(["draft", "validated", "deployed", "error", undefined])("an entry with status %s carries the turn state", status => {
        const entry = buildCandidateEntry(comp({ name: "A", status, origin: "session" }), ctx({ isResponding: responding }), undefined);
        expect(entry.isBuilderResponding).toBe(responding);
    });
});
