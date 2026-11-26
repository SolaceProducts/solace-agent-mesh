// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, describe, expect, test, vi } from "vitest";

import type { CreateAgentRequest } from "@/api/agents";
import { api } from "@/api/client";

import { indexEntries, isAgentFixableError, type PlatformLookupEntry } from "../builderUtils";
import { SAVE_CANCELLED_MESSAGE } from "../saveOnlyPolicy";

import {
    isDeployStatusFlip,
    orderComponentsForSave,
    platformWorkflowId,
    requireRecognizedConfig,
    requireWorkflowListForCreate,
    resolveConnectorIds,
    resolveSkillBindings,
    runPerKindSaves,
    saveAgentComponent,
    type SaveResult,
    type SaveRun,
    saveWorkflowComponent,
    shouldSkipSave,
    toDeployFailureState,
    workflowSaveNames,
} from "./useSaveOrchestration";

type Comp = Parameters<typeof resolveConnectorIds>[0];

const agent = (over: Partial<Comp>): Comp => ({ name: "PetAgent", type: "agent", filename: "agent.yaml", origin: "session", ...over }) as Comp;

const connectorComp = (name: string, origin: "session" | "platform" = "session"): Comp => ({ name, type: "connector", filename: `${name}.json`, origin }) as Comp;

describe("resolveConnectorIds", () => {
    test("resolves a connector created in this save session from the saved map", () => {
        const comp = agent({ connectors: ["pet diet"], depends_on: ["pet diet"] });
        const components = [comp, connectorComp("pet diet")];
        const saved = new Map<string, SaveResult>([["pet diet", { name: "pet diet", type: "connector", id: "session-id" }]]);

        expect(resolveConnectorIds(comp, components, saved, new Map())).toEqual(["session-id"]);
    });

    test("resolves a PRE-EXISTING platform connector via connectorsByName fallback (the regression)", () => {
        const comp = agent({ connectors: ["pet diet"] });
        const connectorsByName = new Map<string, PlatformLookupEntry>([["pet diet", { id: "platform-id" }]]);

        expect(resolveConnectorIds(comp, [comp], new Map(), connectorsByName)).toEqual(["platform-id"]);
    });

    test("matches the platform connector on its normalized name", () => {
        const comp = agent({ connectors: ["pet diet"] });
        const connectorsByName = new Map<string, PlatformLookupEntry>([["petdiet", { id: "platform-id" }]]);

        expect(resolveConnectorIds(comp, [comp], new Map(), connectorsByName)).toEqual(["platform-id"]);
    });

    test("prefers the session-saved id over the platform lookup when both exist", () => {
        const comp = agent({ connectors: ["pet diet"] });
        const saved = new Map<string, SaveResult>([["pet diet", { name: "pet diet", type: "connector", id: "session-id" }]]);
        const connectorsByName = new Map<string, PlatformLookupEntry>([["pet diet", { id: "platform-id" }]]);

        expect(resolveConnectorIds(comp, [comp], saved, connectorsByName)).toEqual(["session-id"]);
    });

    test("ignores a depends_on entry that is not a connector component", () => {
        const comp = agent({ depends_on: ["HelperAgent"] });
        const components = [comp, { name: "HelperAgent", type: "agent", filename: "h.yaml", origin: "platform" } as Comp];
        const connectorsByName = new Map<string, PlatformLookupEntry>([["HelperAgent", { id: "should-not-resolve" }]]);

        expect(resolveConnectorIds(comp, components, new Map(), connectorsByName)).toBeUndefined();
    });

    test("resolves a depends_on connector that IS a manifest connector component", () => {
        const comp = agent({ depends_on: ["pet diet"] });
        const components = [comp, connectorComp("pet diet", "platform")];
        const connectorsByName = new Map<string, PlatformLookupEntry>([["pet diet", { id: "platform-id" }]]);

        expect(resolveConnectorIds(comp, components, new Map(), connectorsByName)).toEqual(["platform-id"]);
    });

    test("returns undefined when nothing resolves so the agent payload omits connectors", () => {
        const comp = agent({ connectors: ["unknown"] });
        expect(resolveConnectorIds(comp, [comp], new Map(), new Map())).toBeUndefined();
    });

    test("dedupes when the same connector is in both connectors and depends_on", () => {
        const comp = agent({ connectors: ["pet diet"], depends_on: ["pet diet"] });
        const components = [comp, connectorComp("pet diet", "platform")];
        const connectorsByName = new Map<string, PlatformLookupEntry>([["pet diet", { id: "platform-id" }]]);

        expect(resolveConnectorIds(comp, components, new Map(), connectorsByName)).toEqual(["platform-id"]);
    });
});

type OrderComp = Parameters<typeof orderComponentsForSave>[0][number];

describe("orderComponentsForSave — a2a_agent", () => {
    const mk = (name: string, type: string): OrderComp => ({ name, type, filename: `${name}.json`, origin: "session" }) as OrderComp;

    test("places a2a_agent in its own bucket between connectors and agents, not in the agents bucket", () => {
        const ordered = orderComponentsForSave([mk("Gw", "gateway"), mk("Wf", "workflow"), mk("Chat", "agent"), mk("WeatherProxy", "a2a_agent"), mk("Db", "connector"), mk("Docs", "skill")]);
        const order = ordered.map(c => c.name);
        expect(order).toEqual(["Docs", "Db", "WeatherProxy", "Chat", "Wf", "Gw"]);
        expect(order.indexOf("WeatherProxy")).toBeLessThan(order.indexOf("Chat"));
    });
});

describe("shouldSkipSave — a2a_agent", () => {
    const proxy = { name: "WeatherProxy", type: "a2a_agent", filename: "p.json", origin: "session" } as OrderComp;
    const stdAgent = { name: "Chat", type: "agent", filename: "a.yaml", origin: "session" } as OrderComp;
    const never = () => false;
    const always = () => true;

    test("a2a_agent is NOT skipped when present in the agent/workflow/gateway platform lookup", () => {
        expect(shouldSkipSave(stdAgent, never, always)).toBe(true);
        expect(shouldSkipSave(proxy, never, always)).toBe(false);
    });

    test("a2a_agent is still skipped when platform-origin or already saved this flow", () => {
        expect(shouldSkipSave({ ...proxy, origin: "platform" } as OrderComp, never, never)).toBe(true);
        expect(shouldSkipSave(proxy, name => name === "WeatherProxy", never)).toBe(true);
    });
});

describe("shouldSkipSave — a skill", () => {
    const skill = { name: "haiku-style", type: "skill", filename: "s.yaml", origin: "session" } as OrderComp;
    const never = () => false;
    const always = () => true;

    test.each([
        ["new to this session and to the platform", never, never, false],
        ["saved earlier in the session", always, never, false],
        ["on the platform and saved earlier in the session", always, always, false],
        ["on the platform", never, always, false],
    ] as const)("is not skipped when %s", (_label, alreadySaved, onPlatform, expected) => {
        expect(shouldSkipSave(skill, alreadySaved, onPlatform)).toBe(expected);
    });

    test("is skipped when platform-origin, whatever else is true", () => {
        expect(shouldSkipSave({ ...skill, origin: "platform" } as OrderComp, never, never)).toBe(true);
    });
});

describe("shouldSkipSave — workflow already on the platform", () => {
    const wf = { name: "Detector", type: "workflow", filename: "wf.yaml", origin: "session" } as OrderComp;
    const never = () => false;
    const always = () => true;

    test("is NOT skipped — it is updated in place instead", () => {
        expect(shouldSkipSave(wf, never, always)).toBe(false);
    });

    test("is NOT skipped even when it was already saved earlier in the session", () => {
        expect(shouldSkipSave(wf, () => true, always)).toBe(false);
        expect(shouldSkipSave(wf, name => name === "Detector", never)).toBe(false);
    });

    test("is still skipped when platform-origin", () => {
        expect(shouldSkipSave({ ...wf, origin: "platform" } as OrderComp, never, never)).toBe(true);
    });

    test("the alreadySaved guard still suppresses a repeat save for other kinds", () => {
        const agent = { name: "Chat", type: "agent", filename: "a.yaml", origin: "session" } as OrderComp;
        expect(shouldSkipSave(agent, () => true, never)).toBe(true);
    });
});

describe("platformWorkflowId", () => {
    type WfComp = Parameters<typeof platformWorkflowId>[0];
    const wf = (over: Partial<WfComp>): WfComp => ({ name: "Detector", type: "workflow", filename: "wf.yaml", origin: "session", ...over }) as WfComp;
    const entry = (id: string, name = "Detector"): PlatformLookupEntry => ({ id, name }) as PlatformLookupEntry;

    test("prefers the freshly fetched platform id over a stale manifest id", () => {
        const lookup = new Map([["Detector", entry("live-id")]]);
        expect(platformWorkflowId(wf({ id: "dead-id" }), lookup)).toBe("live-id");
    });

    test("falls back to the manifest id when the lookup has no entry", () => {
        expect(platformWorkflowId(wf({ id: "wf-1" }), new Map())).toBe("wf-1");
    });

    test("resolves from an exact name match with no manifest id", () => {
        const lookup = new Map([["Detector", entry("wf-1")]]);
        expect(platformWorkflowId(wf({}), lookup)).toBe("wf-1");
    });

    const records = [
        { id: "wf-new", name: "weatherreport" },
        { id: "wf-old", name: "Weather-Report" },
    ];
    test.each([
        ["newer first", records],
        ["older first", [...records].reverse()],
    ])("each of two workflows whose names collide when normalized resolves to itself (%s)", (_order, listing) => {
        const lookup = new Map<string, PlatformLookupEntry>();
        for (const record of listing) indexEntries(lookup, record);

        expect(lookup.get("weatherreport")?.id).toBe("wf-new");
        expect(platformWorkflowId(wf({ name: "weatherreport", id: undefined }), lookup)).toBe("wf-new");
        expect(platformWorkflowId(wf({ name: "Weather-Report", id: undefined }), lookup)).toBe("wf-old");
    });

    test("an exact-name row wins over a cached manifest id that contradicts it", () => {
        const lookup = new Map<string, PlatformLookupEntry>();
        for (const record of records) indexEntries(lookup, record);

        expect(platformWorkflowId(wf({ name: "weatherreport", id: "wf-old" }), lookup)).toBe("wf-new");
        expect(platformWorkflowId(wf({ name: "weatherreport", id: "wf-new" }), lookup)).toBe("wf-new");
    });

    test("a normalized alias still resolves a name no record holds exactly", () => {
        const lookup = new Map<string, PlatformLookupEntry>();
        indexEntries(lookup, { id: "wf-old", name: "Weather-Report" });

        expect(lookup.get("weatherreport")?.id).toBe("wf-old");
    });

    test("keeps a cached id the lookup does not mention", () => {
        const lookup = new Map<string, PlatformLookupEntry>();
        indexEntries(lookup, { id: "wf-other", name: "Unrelated" });
        expect(platformWorkflowId(wf({ name: "Detector", id: "wf-gone" }), lookup)).toBe("wf-gone");
    });

    test("accepts an exact displayName match", () => {
        const lookup = new Map<string, PlatformLookupEntry>();
        indexEntries(lookup, { id: "wf-1", name: "detector_flow", displayName: "Detector Flow" });
        expect(platformWorkflowId(wf({ name: "Detector Flow", id: undefined }), lookup)).toBe("wf-1");
    });

    describe("legacy stored name", () => {
        const legacyName = "mule_detection_workflow";
        const platformName = "MuleDetectionWorkflow";
        const names = { platformName, legacyName };
        const mule = (over: Partial<WfComp>) => wf({ name: platformName, ...over });
        const legacyAppConfig = { agent_name: platformName, nodes: [] };
        const legacyLookup = () => {
            const lookup = new Map<string, PlatformLookupEntry>();
            indexEntries(lookup, { id: "wf-legacy", name: legacyName, appConfig: legacyAppConfig });
            return lookup;
        };

        test("adopts the row stored under the exact legacy name", () => {
            expect(platformWorkflowId(mule({ id: undefined }), legacyLookup(), names)).toBe("wf-legacy");
        });

        test("accepts a cached manifest id the lookup reports under the legacy name", () => {
            expect(platformWorkflowId(mule({ id: "wf-legacy" }), legacyLookup(), names)).toBe("wf-legacy");
            expect(platformWorkflowId(mule({ id: "wf-legacy" }), legacyLookup())).toBeUndefined();
        });

        test("accepts a cached legacy id even when an alias holds the legacy name's key", () => {
            const lookup = new Map([
                [legacyName, entry("wf-other", "mule-detection-workflow")],
                ["legacy-row", { ...entry("wf-legacy", legacyName), agentName: platformName }],
            ]);
            expect(platformWorkflowId(mule({ id: "wf-legacy" }), lookup, names)).toBe("wf-legacy");
        });

        test("prefers a row already stored under the current name", () => {
            const lookup = legacyLookup();
            indexEntries(lookup, { id: "wf-current", name: platformName });
            expect(platformWorkflowId(mule({ id: undefined }), lookup, names)).toBe("wf-current");
        });

        test("prefers the row under the platform name even when the manifest name differs", () => {
            const lookup = legacyLookup();
            indexEntries(lookup, { id: "wf-current", name: platformName });
            expect(platformWorkflowId(wf({ name: "Mule Detection", id: undefined }), lookup, names)).toBe("wf-current");
            expect(platformWorkflowId(wf({ name: "Mule Detection", id: "wf-legacy" }), lookup, names)).toBe("wf-current");
        });

        test("defers to a row the platform treats as the same name", () => {
            const lookup = legacyLookup();
            indexEntries(lookup, { id: "wf-spaced", name: "Mule Detection Workflow" });
            expect(platformWorkflowId(wf({ name: "Mule Detection", id: undefined }), lookup, names)).toBe("wf-spaced");
        });

        test("does not treat punctuation variants as the same platform name", () => {
            const lookup = legacyLookup();
            indexEntries(lookup, { id: "wf-hyphen", name: "Mule-Detection-Workflow" });
            expect(platformWorkflowId(wf({ name: "Mule Detection", id: undefined }), lookup, names)).toBe("wf-legacy");
        });

        test("keeps a cached manifest id the lookup reports under the platform name", () => {
            const lookup = new Map([
                [platformName, entry("wf-alias", "mule-detection-workflow")],
                ["own-row", entry("wf-current", platformName)],
            ]);
            expect(platformWorkflowId(wf({ name: "Mule Detection", id: "wf-current" }), lookup, names)).toBe("wf-current");
        });

        test("refuses a legacy-named row whose stored agent_name is not the platform name", () => {
            const stranger = new Map<string, PlatformLookupEntry>();
            indexEntries(stranger, { id: "wf-stranger", name: legacyName, appConfig: { agent_name: "SomethingElse", nodes: [] } });
            expect(platformWorkflowId(mule({ id: undefined }), stranger, names)).toBeUndefined();
            expect(platformWorkflowId(wf({ name: "Mule Detection", id: "wf-stranger" }), stranger, names)).toBeUndefined();

            const noProvenance = new Map<string, PlatformLookupEntry>();
            indexEntries(noProvenance, { id: "wf-stranger", name: legacyName, appConfig: { nodes: [] } });
            expect(platformWorkflowId(mule({ id: undefined }), noProvenance, names)).toBeUndefined();
            expect(platformWorkflowId(mule({ id: undefined }), noProvenance, { legacyName })).toBeUndefined();
        });

        test("adopts a legacy name two workflows share only for the row's owner", () => {
            const workflowYaml = (agentName: string) => `
apps:
  - name: ${legacyName}
    app_config:
      agent_name: ${agentName}
      agent_type: workflow
      nodes:
        - id: score
          type: agent
          agent_name: MuleRiskScorer
`;
            const ownerNames = workflowSaveNames(workflowYaml(platformName));
            const siblingNames = workflowSaveNames(workflowYaml("MuleDetectionWorkflowV2"));
            expect(ownerNames).toEqual({ platformName, legacyName });
            expect(siblingNames).toEqual({ platformName: "MuleDetectionWorkflowV2", legacyName });
            expect(platformWorkflowId(mule({ id: undefined }), legacyLookup(), ownerNames)).toBe("wf-legacy");
            expect(platformWorkflowId(wf({ name: "MuleDetectionWorkflowV2", id: undefined }), legacyLookup(), siblingNames)).toBeUndefined();
        });

        test("refuses a different row that only shares the legacy name's normalized key", () => {
            const lookup = new Map<string, PlatformLookupEntry>();
            indexEntries(lookup, { id: "wf-other", name: "mule-detection-workflow", appConfig: legacyAppConfig });
            expect(platformWorkflowId(mule({ id: undefined }), lookup, names)).toBeUndefined();

            const aliased = new Map([[legacyName, { ...entry("wf-other", "mule-detection-workflow"), agentName: platformName }]]);
            expect(platformWorkflowId(mule({ id: undefined }), aliased, names)).toBeUndefined();
        });
    });

    test("is undefined for every non-workflow kind", () => {
        const lookup = new Map([["Detector", entry("wf-1")]]);
        expect(platformWorkflowId(wf({ type: "agent" }), lookup)).toBeUndefined();
        expect(platformWorkflowId(wf({ type: "gateway" }), lookup)).toBeUndefined();
    });
});

describe("saveWorkflowComponent", () => {
    const yaml = `apps:
  - name: Detector
    app_config:
      agent_name: Detector
      agent_type: workflow
      description: "detects things"
      nodes: []
      output_mapping: {}
`;

    test("PUTs when the workflow already exists, and reuses the existing id", async () => {
        const createWorkflow = { mutateAsync: vi.fn() };
        const updateWorkflow = { mutateAsync: vi.fn().mockResolvedValue({ id: "wf-1" }) };

        const result = await saveWorkflowComponent(yaml, "Detector", createWorkflow, "wf-1", updateWorkflow);

        expect(createWorkflow.mutateAsync).not.toHaveBeenCalled();
        expect(updateWorkflow.mutateAsync).toHaveBeenCalledTimes(1);
        expect(updateWorkflow.mutateAsync.mock.calls[0][0]).toMatchObject({ id: "wf-1" });
        expect(result).toEqual({ name: "Detector", type: "workflow", id: "wf-1" });
    });

    test("POSTs when the workflow is new", async () => {
        const createWorkflow = { mutateAsync: vi.fn().mockResolvedValue({ id: "wf-new" }) };
        const updateWorkflow = { mutateAsync: vi.fn() };

        const result = await saveWorkflowComponent(yaml, "Detector", createWorkflow, undefined, updateWorkflow);

        expect(updateWorkflow.mutateAsync).not.toHaveBeenCalled();
        expect(createWorkflow.mutateAsync).toHaveBeenCalledTimes(1);
        expect(result).toEqual({ name: "Detector", type: "workflow", id: "wf-new" });
    });

    test("sends the same payload either way, so an update cannot ship a different shape than a create", async () => {
        const createWorkflow = { mutateAsync: vi.fn().mockResolvedValue({ id: "wf-new" }) };
        const updateWorkflow = { mutateAsync: vi.fn().mockResolvedValue({ id: "wf-1" }) };

        await saveWorkflowComponent(yaml, "Detector", createWorkflow, undefined, updateWorkflow);
        await saveWorkflowComponent(yaml, "Detector", createWorkflow, "wf-1", updateWorkflow);

        expect(updateWorkflow.mutateAsync.mock.calls[0][0].payload).toEqual(createWorkflow.mutateAsync.mock.calls[0][0]);
    });
});

describe("resolveSkillBindings", () => {
    const emptyLookup = { customIdsByName: new Map<string, string>(), builtinNames: new Set<string>() };

    test("a skill saved earlier in this flow binds by its new platform id (skillIds)", () => {
        const saved = new Map<string, SaveResult>([["report-skill", { name: "report-skill", type: "skill", id: "skill-uuid-1" }]]);
        expect(resolveSkillBindings(["report-skill"], saved, emptyLookup)).toEqual({ skillIds: ["skill-uuid-1"] });
    });

    test("a pre-existing platform custom skill binds by id from the lookup", () => {
        const lookup = { customIdsByName: new Map([["prior-skill", "skill-uuid-2"]]), builtinNames: new Set<string>() };
        expect(resolveSkillBindings(["prior-skill"], new Map(), lookup)).toEqual({ skillIds: ["skill-uuid-2"] });
    });

    test("a built-in skill binds by name via skillRefs, never skillIds", () => {
        const lookup = { customIdsByName: new Map<string, string>(), builtinNames: new Set(["sam-templatize"]) };
        expect(resolveSkillBindings(["sam-templatize"], new Map(), lookup)).toEqual({ skillRefs: ["sam-templatize"] });
    });

    test("mixes custom ids and built-in refs across one skills list", () => {
        const saved = new Map<string, SaveResult>([["report-skill", { name: "report-skill", type: "skill", id: "skill-uuid-1" }]]);
        const lookup = { customIdsByName: new Map([["prior-skill", "skill-uuid-2"]]), builtinNames: new Set(["sam-templatize"]) };
        expect(resolveSkillBindings(["report-skill", "prior-skill", "sam-templatize"], saved, lookup)).toEqual({
            skillIds: ["skill-uuid-1", "skill-uuid-2"],
            skillRefs: ["sam-templatize"],
        });
    });

    test("a same-name NON-skill save result does not bind (agent named like a skill)", () => {
        const saved = new Map<string, SaveResult>([["helper", { name: "helper", type: "agent", id: "agent-uuid" }]]);
        expect(() => resolveSkillBindings(["helper"], saved, emptyLookup)).toThrow(/Cannot bind skill/);
    });

    test("an unresolvable name fails the save loudly instead of deploying a broken load_skill", () => {
        expect(() => resolveSkillBindings(["ghost-skill"], new Map(), emptyLookup)).toThrow(/"ghost-skill"/);
    });

    test("no names → no bindings", () => {
        expect(resolveSkillBindings(undefined, new Map(), emptyLookup)).toEqual({});
        expect(resolveSkillBindings([], new Map(), emptyLookup)).toEqual({});
    });
});

describe("isDeployStatusFlip", () => {
    test("components moving to deployed, from any status, are a deploy's own writes", () => {
        expect(isDeployStatusFlip("A:validated|B:validated", "A:deployed|B:validated")).toBe(true);
        expect(isDeployStatusFlip("A:validated|B:validated", "A:deployed|B:deployed")).toBe(true);
        for (const status of ["draft", "error", "blocked", "undefined"]) {
            expect(isDeployStatusFlip(`A:${status}|B:validated`, "A:deployed|B:validated")).toBe(true);
        }
    });

    test("anything else is a build turn", () => {
        expect(isDeployStatusFlip("A:deployed", "A:validated")).toBe(false);
        expect(isDeployStatusFlip("A:validated|B:validated", "A:deployed|B:failed")).toBe(false);
        expect(isDeployStatusFlip("A:validated", "A:deployed|B:validated")).toBe(false);
        expect(isDeployStatusFlip("A:validated|B:validated", "A:deployed")).toBe(false);
        expect(isDeployStatusFlip("A:validated", "C:deployed")).toBe(false);
        expect(isDeployStatusFlip("A:validated|B:deployed", "B:deployed|A:deployed")).toBe(false);
        expect(isDeployStatusFlip("A:validated", "")).toBe(false);
        expect(isDeployStatusFlip("A:revalidated", "A:redeployed")).toBe(false);
    });

    test("an unchanged or missing signature is not a flip", () => {
        expect(isDeployStatusFlip("A:validated", "A:validated")).toBe(false);
        expect(isDeployStatusFlip(undefined, "A:deployed")).toBe(false);
        expect(isDeployStatusFlip("A:validated", undefined)).toBe(false);
        expect(isDeployStatusFlip("", "A:deployed")).toBe(false);
    });
});

describe("toDeployFailureState", () => {
    test("no error → none", () => {
        expect(toDeployFailureState(null, false, false)).toEqual({ kind: "none" });
        expect(toDeployFailureState(null, true, true)).toEqual({ kind: "none" });
    });
    test("error + auto-fix in flight → fixing (agent owns recovery, no manual retry)", () => {
        expect(toDeployFailureState("bad yaml", true, false)).toEqual({ kind: "fixing", message: "bad yaml" });
    });
    test("error not dispatched to the agent → retryable", () => {
        expect(toDeployFailureState("deploy failed: 500", false, false)).toEqual({ kind: "retryable", message: "deploy failed: 500" });
    });
    test("exhausted auto-fix → retryable (even if submitted is still set)", () => {
        expect(toDeployFailureState("bad yaml", true, true)).toEqual({ kind: "retryable", message: "bad yaml" });
    });

    test("blocked outranks retryable and fixing", () => {
        expect(toDeployFailureState("plan unreadable", false, false, true)).toEqual({ kind: "blocked", message: "plan unreadable" });
        expect(toDeployFailureState("plan unreadable", true, false, true)).toEqual({ kind: "blocked", message: "plan unreadable" });
        expect(toDeployFailureState("plan unreadable", true, true, true)).toEqual({ kind: "blocked", message: "plan unreadable" });
    });

    test("blocked without an error is still none", () => {
        expect(toDeployFailureState(null, false, false, true)).toEqual({ kind: "none" });
    });

    test("the cancelled-save text is declined, whatever the auto-fix state, and any other text with it is not", () => {
        expect(toDeployFailureState(SAVE_CANCELLED_MESSAGE, false, false)).toEqual({ kind: "declined", message: SAVE_CANCELLED_MESSAGE });
        expect(toDeployFailureState(SAVE_CANCELLED_MESSAGE, true, false)).toEqual({ kind: "declined", message: SAVE_CANCELLED_MESSAGE });
        expect(toDeployFailureState(SAVE_CANCELLED_MESSAGE, true, true)).toEqual({ kind: "declined", message: SAVE_CANCELLED_MESSAGE });
        expect(toDeployFailureState(`bad gateway\n${SAVE_CANCELLED_MESSAGE}`, false, false)).toEqual({ kind: "retryable", message: `bad gateway\n${SAVE_CANCELLED_MESSAGE}` });
    });

    test("blocked outranks declined", () => {
        expect(toDeployFailureState(SAVE_CANCELLED_MESSAGE, false, false, true)).toEqual({ kind: "blocked", message: SAVE_CANCELLED_MESSAGE });
    });

    test("defaults to not blocked, so existing callers are unchanged", () => {
        expect(toDeployFailureState("deploy failed: 500", false, false)).toEqual({ kind: "retryable", message: "deploy failed: 500" });
    });
});

describe("saveAgentComponent — create payload", () => {
    const page = <T,>(data: T[]) => ({ data, meta: { pagination: { pageNumber: 1, nextPage: null } } });

    const builtinCatalog = [
        {
            id: "builtin_web_request_tools",
            name: "Web Request Tools",
            aliases: ["web_tools"],
            tools: [{ toolName: "web_request" }, { toolName: "web_search_google" }],
        },
        { id: "builtin_artifact_tools", name: "Artifact Tools", aliases: ["artifact_management"], tools: [{ toolName: "list_artifacts" }] },
    ];

    const toolsetV2 = ({ id, name, aliases, tools }: (typeof builtinCatalog)[number]) => ({
        id,
        type: "toolset",
        origin: "builtin",
        name,
        description: null,
        runtime: null,
        discoveryStatus: null,
        discoveryErrors: [],
        bundle: null,
        tools: tools.map(({ toolName }) => ({ toolName, description: null, timeoutSeconds: null, sandboxProfile: null, parameters: null, configSchema: null })),
        agents: [],
        configSummary: null,
        capabilities: {},
        default: false,
        aliases,
        createdBy: null,
        updatedBy: null,
        createdTime: null,
        updatedTime: null,
    });

    const stubPlatform = () =>
        vi.spyOn(api.platform, "get").mockImplementation((async (path: string) => new Response(JSON.stringify(page(path.includes("/toolsets") ? builtinCatalog.map(toolsetV2) : [])), { status: 200 })) as typeof api.platform.get);

    const agentYaml = (toolsBlock: string) => `
apps:
  - name: DocFetcher
    app_config:
      agent_name: DocFetcher
      instruction: |
        ${"x".repeat(150)}
      agent_card:
        description: Fetches and summarizes pages from docs.solace.com.
${toolsBlock}
`;

    const saveAndCapture = async (yamlText: string) => {
        const captured: CreateAgentRequest[] = [];
        const createAgent = {
            mutateAsync: async (payload: CreateAgentRequest) => {
                captured.push(payload);
                return { id: "agent-1" };
            },
        };
        const result = await saveAgentComponent(yamlText, "DocFetcher", undefined, createAgent, new Map());
        return { payload: captured[0], result };
    };

    afterEach(() => vi.restoreAllMocks());

    test("an individual builtin tool reaches the API as its toolset plus exclusions", async () => {
        stubPlatform();

        const { payload, result } = await saveAndCapture(
            agentYaml(`      tools:
        - tool_type: builtin
          tool_name: web_request`)
        );

        expect(payload.toolsets).toEqual(["builtin_web_request_tools"]);
        expect(payload.toolsetExcludedTools).toEqual({ builtin_web_request_tools: ["web_search_google"] });
        expect(result).toEqual({ name: "DocFetcher", type: "agent", id: "agent-1" });
    });

    test("the built-in catalog comes from /api/v2", async () => {
        const get = stubPlatform();

        const { payload } = await saveAndCapture(
            agentYaml(`      tools:
        - tool_type: builtin
          tool_name: web_request`)
        );

        expect(payload.toolsets).toEqual(["builtin_web_request_tools"]);
        expect(payload.toolsetExcludedTools).toEqual({ builtin_web_request_tools: ["web_search_google"] });
        const paths = get.mock.calls.map(([path]) => String(path));
        expect(paths.some(path => path.startsWith("/api/v2/platform/toolsets?origin=builtin"))).toBe(true);
        expect(paths.filter(path => path.startsWith("/api/v1/"))).toEqual([]);
    });

    test("an aliased builtin-group reaches the API as the platform toolset id", async () => {
        stubPlatform();

        const { payload } = await saveAndCapture(
            agentYaml(`      tools:
        - tool_type: builtin-group
          group_name: web_tools`)
        );

        expect(payload.toolsets).toEqual(["builtin_web_request_tools"]);
        expect(payload.toolsetExcludedTools).toBeUndefined();
    });

    test("a hand-authored toolsets list survives alongside a derived one", async () => {
        stubPlatform();

        const { payload } = await saveAndCapture(
            agentYaml(`      toolsets:
        - my_custom_package
      tools:
        - tool_type: builtin
          tool_name: web_request`)
        );

        expect(payload.toolsets).toContain("my_custom_package");
        expect(payload.toolsets).toContain("builtin_web_request_tools");
    });

    test("the extraction-only fields never reach the strict-decoding API", async () => {
        stubPlatform();

        const { payload } = await saveAndCapture(
            agentYaml(`      tools:
        - tool_type: builtin
          tool_name: web_request`)
        );

        expect(payload).not.toHaveProperty("builtinGroupNames");
        expect(payload).not.toHaveProperty("builtinToolNames");
        expect(payload).not.toHaveProperty("loadableSkillNames");
    });

    test("fails the save when the toolset catalog is unreachable", async () => {
        const err = vi.spyOn(console, "error").mockImplementation(() => {});
        vi.spyOn(api.platform, "get").mockImplementation((async (_path: string) => {
            throw new Error("boom");
        }) as typeof api.platform.get);

        await expect(
            saveAndCapture(
                agentYaml(`      tools:
        - tool_type: builtin
          tool_name: web_request`)
            )
        ).rejects.toThrow(/catalog could not be loaded/);
        err.mockRestore();
    });

    test("a config with no builtin tools sends no toolsets at all", async () => {
        stubPlatform();

        const { payload } = await saveAndCapture(agentYaml(`      input_modes: [text]`));

        expect(payload.toolsets).toBeUndefined();
        expect(payload.toolsetExcludedTools).toBeUndefined();
    });

    test("agent_card skills reach the API with their id, tags and examples", async () => {
        stubPlatform();

        const { payload } = await saveAndCapture(
            agentYaml(`        skills:
          - id: fetch-docs
            name: Fetch Docs
            description: Fetches a docs page
            tags: [docs, fetch]
            examples: ["Summarize the broker install page"]`)
        );

        expect(payload.skills).toEqual([{ id: "fetch-docs", name: "Fetch Docs", description: "Fetches a docs page", tags: ["docs", "fetch"], examples: ["Summarize the broker install page"] }]);
    });
});

describe("runPerKindSaves", () => {
    type RunComp = Parameters<typeof runPerKindSaves>[1][number];
    const mk = (name: string, type: string): RunComp => ({ name, type, filename: `${name}.yaml`, origin: "session" }) as RunComp;

    const makeRun = (ordered: RunComp[], configs: Record<string, string | undefined>, seeded: string[] = []) => {
        const results = new Map<string, SaveResult>(seeded.map(n => [n, { name: n, type: "workflow", id: `${n}-id` } as SaveResult]));
        const saveOne = vi.fn(async (_yaml: string, comp: RunComp) => ({ name: comp.name, type: comp.type, id: `${comp.name}-id` }) as SaveResult);
        const attributed: (string | null)[] = [];
        const run = {
            changed: [],
            declined: [],
            notWritten: [],
            ordered,
            results,
            skip: () => false,
            attribute: (c: RunComp | null) => attributed.push(c?.name ?? null),
            fetchConfigByFilename: (async (filename: string) => configs[filename]) as SaveRun["fetchConfigByFilename"],
            saveOne,
        } as unknown as SaveRun;
        return { run, saveOne, attributed };
    };

    test("an unresolvable config becomes a partial failure and the rest of the loop still saves", async () => {
        const wf = mk("Detector", "workflow");
        const gw = mk("Webhook", "gateway");
        const { run, saveOne } = makeRun([wf, gw], { "Detector.yaml": undefined, "Webhook.yaml": "kind: gateway" });

        const outcome = await runPerKindSaves(run, [wf, gw], new Map());

        expect(outcome.partialFailures?.map(f => f.name)).toEqual(["Detector"]);
        expect(saveOne).toHaveBeenCalledTimes(1);
        expect(saveOne.mock.calls[0][1].name).toBe("Webhook");
        expect(outcome.writtenCount).toBe(1);
    });

    test("counts a re-saved workflow already present in the seeded results map", async () => {
        const wf = mk("Detector", "workflow");
        const { run } = makeRun([wf], { "Detector.yaml": "kind: workflow" }, ["Detector"]);

        const outcome = await runPerKindSaves(run, [wf], new Map());

        expect(outcome.writtenCount).toBe(1);
        expect(outcome.partialFailures).toBeUndefined();
    });
});

describe("requireRecognizedConfig", () => {
    const entrypointFile = ["kind: entrypoint", "name: mule_gateway", "spec:", "  type: event_mesh", "  values:", "    event_rules: []"].join("\n");
    const runtimeGateway = ["apps:", "  - name: mule_gateway", "    app_config:", "      event_handlers:", "        - name: h", "          target_workflow_name: W"].join("\n");

    test("names the shape a gateway must have when the config is unrecognizable", () => {
        expect(() => requireRecognizedConfig(entrypointFile, { name: "mule_gateway", type: "gateway" })).toThrow(/"mule_gateway".*event_handlers.*sam config apply/s);
    });

    test("returns the detected type for a recognizable config", () => {
        expect(requireRecognizedConfig(runtimeGateway, { name: "mule_gateway", type: "gateway" })).toBe("gateway");
    });

    test("fails the per-kind save instead of reporting it written", async () => {
        const gw = { name: "mule_gateway", type: "gateway", filename: "mule_gateway.yaml", origin: "session" } as Parameters<typeof runPerKindSaves>[1][number];
        const run = {
            changed: [],
            declined: [],
            notWritten: [],
            ordered: [gw],
            results: new Map<string, SaveResult>(),
            skip: () => false,
            attribute: () => {},
            fetchConfigByFilename: (async () => entrypointFile) as SaveRun["fetchConfigByFilename"],
            saveOne: async (yamlText: string, comp: typeof gw) => {
                requireRecognizedConfig(yamlText, comp);
                return { name: comp.name, type: "gateway", id: "id" } as SaveResult;
            },
        } as unknown as SaveRun;

        await expect(runPerKindSaves(run, [gw], new Map())).rejects.toThrow(/not a recognized component config/);
    });
});

describe("requireWorkflowListForCreate", () => {
    test.each([
        ["a workflow with no resolved row and no list", "workflow", undefined, false, true],
        ["a workflow with no resolved row and a list", "workflow", undefined, true, false],
        ["a workflow resolved to a row, whatever the list", "workflow", "wf-1", false, false],
        ["an agent with no list", "agent", undefined, false, false],
    ])("%s", (_name, type, platformId, listed, wantRefusal) => {
        let thrown: unknown;
        try {
            requireWorkflowListForCreate(type, platformId, listed, "Detector");
        } catch (error) {
            thrown = error;
        }
        expect(thrown !== undefined).toBe(wantRefusal);
        if (wantRefusal) {
            expect(isAgentFixableError(thrown)).toBe(false);
            expect((thrown as Error).message).toContain("Detector");
        }
    });
});
