// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it, vi } from "vitest";

import type { BuildOperationResult } from "@/api/builder/applyTypes";

import type { OwnsResource } from "./builderOwnership";
import type { BuildManifest } from "./hooks/useBuilderManifest";
import { componentNamedIn, connectorNamesFor, isServerApplyComponent, plannedOverwrites, platformMatchedNames, saveWarnings, serverApplyKind, toBuildComponentInputs, updatedInPlaceNames, writtenResources } from "./serverApply";

type ManifestComponent = NonNullable<BuildManifest["components"]>[number];

const comp = (over: Partial<ManifestComponent>): ManifestComponent => ({ name: "c", filename: "c.yaml", type: "agent", ...over }) as ManifestComponent;

describe("serverApplyKind", () => {
    it("maps every kind the endpoint persists", () => {
        for (const type of ["connector", "a2a_agent", "agent", "workflow", "gateway"] as const) {
            expect(serverApplyKind(comp({ type }))).toBe(type);
        }
    });

    it("excludes skills, which the frontend still saves itself", () => {
        expect(serverApplyKind(comp({ type: "skill" }))).toBeNull();
    });

    it("returns null for an unrecognized kind rather than assuming agent", () => {
        expect(serverApplyKind(comp({ type: "toolset" as ManifestComponent["type"] }))).toBeNull();
    });
});

describe("isServerApplyComponent", () => {
    it("skips platform-origin components", () => {
        expect(isServerApplyComponent(comp({ origin: "platform" }))).toBe(false);
        expect(isServerApplyComponent(comp({ origin: "session" }))).toBe(true);
    });
});

describe("connectorNamesFor", () => {
    it("takes the connectors list verbatim", () => {
        const c = comp({ connectors: ["db", "slack"] });
        expect(connectorNamesFor(c, [c])).toEqual(["db", "slack"]);
    });

    it("adds connector-typed depends_on entries", () => {
        const connector = comp({ name: "db", type: "connector" });
        const agent = comp({ name: "a", depends_on: ["db"] });
        expect(connectorNamesFor(agent, [connector, agent])).toEqual(["db"]);
    });

    it("ignores depends_on entries that are not connectors", () => {
        const peer = comp({ name: "peer", type: "agent" });
        const agent = comp({ name: "a", depends_on: ["peer"] });
        expect(connectorNamesFor(agent, [peer, agent])).toEqual([]);
    });

    it("de-duplicates a name present in both sources", () => {
        const connector = comp({ name: "db", type: "connector" });
        const agent = comp({ name: "a", connectors: ["db"], depends_on: ["db"] });
        expect(connectorNamesFor(agent, [connector, agent])).toEqual(["db"]);
    });
});

describe("toBuildComponentInputs", () => {
    const fetchOk = vi.fn(async (filename: string) => `content of ${filename}`);

    it("builds one input per persisted component and skips the rest", async () => {
        const components = [
            comp({ name: "db", type: "connector", filename: "db.json" }),
            comp({ name: "sk", type: "skill", filename: "sk.yaml" }),
            comp({ name: "ag", type: "agent", filename: "ag.yaml" }),
            comp({ name: "existing", type: "agent", filename: "e.yaml", origin: "platform" }),
        ];
        const got = await toBuildComponentInputs(components, fetchOk);

        expect(got.map(c => c.name)).toEqual(["db", "ag"]);
        expect(got[0].content).toBe("content of db.json");
        expect(got[0].sourceFilename).toBe("db.json");
    });

    it("sends the platform id the manifest records, so a rename lands on the resource it renames", async () => {
        const id = "01a15453-e34d-7330-a292-50320e36ef02";
        const components = [comp({ name: "Refund Handler", type: "agent", filename: "a.yaml", id }), comp({ name: "Fresh", type: "agent", filename: "f.yaml" })];
        const got = await toBuildComponentInputs(components, fetchOk);

        expect(got.find(c => c.name === "Refund Handler")?.resourceId).toBe(id);
        expect(got.find(c => c.name === "Fresh")).not.toHaveProperty("resourceId");
    });

    it("sends the id for a workflow and for no other kind, which the endpoint matches by name alone", async () => {
        const id = "01a15453-e34d-7330-a292-50320e36ef02";
        const components = ["workflow", "connector", "gateway", "a2a_agent"].map(type => comp({ name: type, type, filename: `${type}.yaml`, id }));
        const got = await toBuildComponentInputs(components, fetchOk);

        expect(got.filter(c => "resourceId" in c).map(c => c.kind)).toEqual(["workflow"]);
    });

    it("does not send an id that cannot be one the platform issued", async () => {
        const got = await toBuildComponentInputs([comp({ name: "ag", type: "agent", filename: "a.yaml", id: "not-an-id" })], fetchOk);
        expect(got[0]).not.toHaveProperty("resourceId");
    });

    it("does not pre-sort — the server owns dependency order", async () => {
        const components = [comp({ name: "ag", type: "agent", filename: "a.yaml" }), comp({ name: "db", type: "connector", filename: "d.json" })];
        const got = await toBuildComponentInputs(components, fetchOk);
        expect(got.map(c => c.name)).toEqual(["ag", "db"]);
    });

    it("attaches connector names only to agents", async () => {
        const components = [comp({ name: "db", type: "connector", filename: "d.json" }), comp({ name: "ag", type: "agent", filename: "a.yaml", connectors: ["db"] })];
        const got = await toBuildComponentInputs(components, fetchOk);

        expect(got.find(c => c.name === "ag")?.connectors).toEqual(["db"]);
        expect(got.find(c => c.name === "db")?.connectors).toBeUndefined();
    });

    it("does not attach connectors to a workflow that states a dependency on one", async () => {
        const components = [comp({ name: "db", type: "connector", filename: "d.json" }), comp({ name: "wf", type: "workflow", filename: "w.yaml", depends_on: ["db"] })];
        const got = await toBuildComponentInputs(components, fetchOk);

        expect(got.find(c => c.name === "wf")).not.toHaveProperty("connectors");
    });

    it("reports which component it is reading, so a fetch failure is attributed to it", async () => {
        const components = [comp({ name: "first", type: "agent", filename: "1.yaml" }), comp({ name: "second", type: "agent", filename: "2.yaml" }), comp({ name: "third", type: "agent", filename: "3.yaml" })];
        const fetchFailingOnThird = vi.fn(async (filename: string) => (filename === "3.yaml" ? null : "body"));

        const seen: string[] = [];
        await expect(toBuildComponentInputs(components, fetchFailingOnThird, c => seen.push(c.name))).rejects.toThrow(/third/);
        expect(seen.at(-1)).toBe("third");
    });

    it("skips the streaming buffer, which may hold a partial write", async () => {
        const fetchSpy = vi.fn(async () => "body");
        await toBuildComponentInputs([comp({ filename: "a.yaml" })], fetchSpy);
        expect(fetchSpy).toHaveBeenCalledWith("a.yaml", { skipAccumulated: true });
    });

    it("omits connectors when the manifest says nothing about them", async () => {
        const got = await toBuildComponentInputs([comp({ name: "ag", type: "agent", filename: "a.yaml" })], fetchOk);
        expect(got[0]).not.toHaveProperty("connectors");
    });

    it("sends an empty list when the manifest states one, so a detach is expressible", async () => {
        const got = await toBuildComponentInputs([comp({ name: "ag", type: "agent", filename: "a.yaml", connectors: [] })], fetchOk);
        expect(got[0].connectors).toEqual([]);
    });

    it("treats a depends_on that names a connector as stating the field", async () => {
        const db = comp({ name: "db", type: "connector", filename: "db.yaml" });
        const agent = comp({ name: "ag", type: "agent", filename: "a.yaml", depends_on: ["db"] });
        const got = await toBuildComponentInputs([db, agent], fetchOk);
        expect(got[1].connectors).toEqual(["db"]);
    });

    it("unions a stated connectors with a depends_on that names another connector", async () => {
        const db = comp({ name: "db", type: "connector", filename: "db.yaml" });
        const cache = comp({ name: "cache", type: "connector", filename: "c.yaml" });
        const agent = comp({ name: "ag", type: "agent", filename: "a.yaml", connectors: ["db"], depends_on: ["cache"] });
        const got = await toBuildComponentInputs([db, cache, agent], fetchOk);
        expect(got[2].connectors?.slice().sort()).toEqual(["cache", "db"]);
    });

    it("does not let a depends_on naming no connector speak for the field", async () => {
        const peer = comp({ name: "peer", type: "agent", filename: "p.yaml" });
        const empty = comp({ name: "ag", type: "agent", filename: "a.yaml", depends_on: [] });
        const onPeer = comp({ name: "ag2", type: "agent", filename: "b.yaml", depends_on: ["peer"] });
        const got = await toBuildComponentInputs([peer, empty, onPeer], fetchOk);
        expect(got[1]).not.toHaveProperty("connectors");
        expect(got[2]).not.toHaveProperty("connectors");
    });

    it("still omits connectors for kinds that have none", async () => {
        const db = comp({ name: "db", type: "connector", filename: "d.yaml" });
        const wf = comp({ name: "wf", type: "workflow", filename: "w.yaml", connectors: ["db"] });
        const got = await toBuildComponentInputs([db, wf], fetchOk);
        expect(got[1]).not.toHaveProperty("connectors");
    });

    it("throws when a config cannot be resolved", async () => {
        const fetchNull = vi.fn(async () => null);
        await expect(toBuildComponentInputs([comp({ name: "ag", filename: "a.yaml" })], fetchNull)).rejects.toThrow(/Could not resolve config for "ag"/);
    });
});

describe("saveWarnings", () => {
    const op = (over: Partial<BuildOperationResult>): BuildOperationResult => ({ kind: "agent", name: "a", action: "create", status: "succeeded", ...over });
    const streaming = "agent config key supports_streaming is not saved by the Builder; the agent keeps the value already stored on the platform, or the default if none";

    it("raises one warning per distinct message, naming every component it applies to", () => {
        const warnings = saveWarnings([op({ name: "Triage", warnings: [streaming] }), op({ name: "Responder", action: "update", warnings: [streaming, "renames x"] })]);
        expect(warnings).toEqual([`Triage, Responder: ${streaming}`, "Responder: renames x"]);
    });

    it("keeps an unchanged component's warning, which is how the server reports an edit it declined", () => {
        const notApplied = "not applied without connector:*:update, and left unchanged: password";
        expect(saveWarnings([op({ kind: "connector", name: "db", action: "unchanged", warnings: [notApplied] })])).toEqual([`db: ${notApplied}`]);
    });

    it("names a component once when it carries the same warning twice", () => {
        expect(saveWarnings([op({ name: "Triage", warnings: [streaming, streaming] })])).toEqual([`Triage: ${streaming}`]);
    });

    it("keeps a failed component's warnings", () => {
        expect(saveWarnings([op({ name: "Triage", status: "failed", warnings: [streaming] })])).toEqual([`Triage: ${streaming}`]);
    });
});

describe("componentNamedIn", () => {
    const components = [comp({ name: "db" }), comp({ name: "Triage" }), comp({ name: "Responder" })];

    it("names the component a plan-level rejection quotes", () => {
        expect(componentNamedIn('agent "Responder": unknown toolset "foo"', components)?.name).toBe("Responder");
    });

    it("prefers the component quoted first over one it references", () => {
        expect(componentNamedIn('agent "Triage": no connector named "db" exists', components)?.name).toBe("Triage");
    });

    it("returns null when the message quotes no component", () => {
        expect(componentNamedIn("the platform was briefly unavailable", components)).toBeNull();
    });
});

describe("platformMatchedNames", () => {
    const op = (over: Partial<BuildOperationResult>): BuildOperationResult => ({ kind: "agent", name: "a", action: "unchanged", status: "succeeded", ...over });

    it("names only what the save proved the platform already matches", () => {
        const names = platformMatchedNames([op({ name: "Matched" }), op({ name: "Declined", changesDeclined: true }), op({ name: "Updated", action: "update" }), op({ name: "Failed", status: "failed" }), op({ name: "Skipped", status: "skipped" })]);
        expect(names).toEqual(["Matched"]);
    });
});

describe("plannedOverwrites for a rename", () => {
    const rename = { kind: "agent", name: "Billing", action: "update", status: "planned", resourceId: "id-A", renamedFrom: "RefundOrchestrator", changedFields: ["name"] } as BuildOperationResult;

    it("names the stored resource being renamed, not only the name it will have", () => {
        expect(plannedOverwrites([rename], () => false)).toEqual([{ name: "RefundOrchestrator → Billing", kind: "agent", changedFields: ["name"] }]);
    });

    it("shows the name the stored resource will carry, which is the resolved name, not the one the manifest uses", () => {
        expect(plannedOverwrites([{ ...rename, name: "Billing", resolvedName: "billing_agent" }], () => false).map(o => o.name)).toEqual(["RefundOrchestrator → billing_agent"]);
    });

    it("names an update that renames nothing by its name alone", () => {
        expect(plannedOverwrites([{ ...rename, renamedFrom: undefined }], () => false).map(o => o.name)).toEqual(["Billing"]);
    });
});

describe("plannedOverwrites", () => {
    const op = (over: Partial<BuildOperationResult>): BuildOperationResult => ({ kind: "agent", name: "A", action: "update", status: "planned", resourceId: "id-A", ...over }) as BuildOperationResult;
    const written =
        (...ids: string[]): OwnsResource =>
        ({ id }) =>
            ids.includes(id);

    it("reports an update to a resource this session has not written, with the fields that change", () => {
        const result = plannedOverwrites([op({ changedFields: ["systemPrompt", "skills"] })], written());
        expect(result).toEqual([{ name: "A", kind: "agent", changedFields: ["systemPrompt", "skills"] }]);
    });

    it("leaves out an update to the very resource this session wrote", () => {
        expect(plannedOverwrites([op({})], written("id-A"))).toEqual([]);
    });

    it("takes the resource id for ownership, not the name, so a renamed component is still the Builder's", () => {
        expect(plannedOverwrites([op({ name: "Renamed", resourceId: "id-A" })], written("id-A"))).toEqual([]);
    });

    it("does not take a matching NAME for ownership: somebody else's resource asks", () => {
        expect(plannedOverwrites([op({ resourceId: "someone-elses" })], written("id-A"))).toHaveLength(1);
    });

    it("asks about an update that names no resource, since nothing shows it is the Builder's", () => {
        expect(plannedOverwrites([op({ resourceId: undefined })], written("id-A"))).toHaveLength(1);
        expect(plannedOverwrites([op({ resourceId: undefined })], written())).toHaveLength(1);
    });

    it("asks the ownership check about the kind of each resource, so a connector can lapse where an agent does not", () => {
        const owns = vi.fn<OwnsResource>().mockReturnValue(false);
        plannedOverwrites([op({ name: "Db", kind: "connector", resourceId: "id-db" }), op({ name: "Triage", kind: "agent", resourceId: "id-agent" })], owns);
        expect(owns.mock.calls.map(([resource]) => resource)).toEqual([
            { id: "id-db", kind: "connector" },
            { id: "id-agent", kind: "agent" },
        ]);
    });

    it("asks about a connector whose ownership has lapsed, and not about an agent the session still owns", () => {
        const owns: OwnsResource = ({ kind }) => kind === "agent";
        const result = plannedOverwrites([op({ name: "Db", kind: "connector", resourceId: "id-db" }), op({ name: "Triage", kind: "agent", resourceId: "id-agent" })], owns);
        expect(result.map(o => o.name)).toEqual(["Db"]);
    });

    it("ignores creates, unchanged rows and anything that is not a plan", () => {
        const result = plannedOverwrites([op({ name: "N", action: "create" }), op({ name: "U", action: "unchanged" }), op({ name: "S", status: "succeeded" })], written());
        expect(result).toEqual([]);
    });

    it("keeps each update it cannot place when some are the Builder's own", () => {
        const result = plannedOverwrites([op({ name: "Mine", resourceId: "id-1" }), op({ name: "Theirs", resourceId: "id-2" })], written("id-1"));
        expect(result.map(o => o.name)).toEqual(["Theirs"]);
        expect(result[0].changedFields).toEqual([]);
    });

    it("names kinds the way users do", () => {
        const result = plannedOverwrites([op({ name: "G", kind: "gateway" }), op({ name: "R", kind: "a2a_agent" }), op({ name: "W", kind: "workflow" })], written());
        expect(result.map(o => o.kind)).toEqual(["entrypoint", "remote agent", "workflow"]);
    });
});

describe("writtenResources", () => {
    it("lists the resources of creates and updates that landed", () => {
        const ops = [
            { kind: "agent", name: "Created", action: "create", status: "succeeded", resourceId: "id-1" },
            { kind: "agent", name: "Updated", action: "update", status: "succeeded", resourceId: "id-2" },
            { kind: "agent", name: "Same", action: "unchanged", status: "succeeded", resourceId: "id-3" },
            { kind: "agent", name: "FailedUpdate", action: "update", status: "failed", resourceId: "id-4" },
            { kind: "agent", name: "NoId", action: "create", status: "succeeded" },
        ] as BuildOperationResult[];
        expect(writtenResources(ops)).toEqual([
            { id: "id-1", kind: "agent" },
            { id: "id-2", kind: "agent" },
        ]);
    });

    it("carries the kind of each, so a connector can be stamped and an agent not", () => {
        const ops = [
            { kind: "connector", name: "Db", action: "create", status: "succeeded", resourceId: "id-db" },
            { kind: "workflow", name: "Flow", action: "update", status: "succeeded", resourceId: "id-flow" },
        ] as BuildOperationResult[];
        expect(writtenResources(ops).map(w => w.kind)).toEqual(["connector", "workflow"]);
    });
});

describe("updatedInPlaceNames", () => {
    it("names only updates that landed", () => {
        const ops = [
            { kind: "agent", name: "Updated", action: "update", status: "succeeded" },
            { kind: "agent", name: "Created", action: "create", status: "succeeded" },
            { kind: "agent", name: "FailedUpdate", action: "update", status: "failed" },
            { kind: "agent", name: "Same", action: "unchanged", status: "succeeded" },
        ] as BuildOperationResult[];
        expect(updatedInPlaceNames(ops)).toEqual(["Updated"]);
    });
});
