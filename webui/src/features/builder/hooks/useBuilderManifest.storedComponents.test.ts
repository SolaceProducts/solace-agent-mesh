// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import yaml from "js-yaml";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { type BuildManifest, isSameComponent, SAM_BUILD_MANIFEST_MIME_TYPE } from "./useBuilderManifest";

function buildQueryWrapper() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

type Component = { filename: string; type: string; name: string; status: string; id?: string };

const component = (name: string, type = "agent", status = "validated", id?: string): Component => ({ filename: `${name}.yaml`, type, name, status, ...(id ? { id } : {}) });

const ID_B = "01a14000-0000-7000-8000-00000000000b";

const manifestYaml = (components: Component[]) => yaml.dump({ name: "my-build", components });

const uploadedComponents = (upload: ReturnType<typeof vi.fn>): Array<Record<string, unknown>> => {
    const doc = yaml.load(upload.mock.calls[0][0].yamlText) as { components: Array<Record<string, unknown>> };
    return doc.components;
};

const markAllDeployed = (prev: BuildManifest): BuildManifest => ({ ...prev, components: (prev.components ?? []).map(c => ({ ...c, status: "deployed" })) });

describe("useBuilderManifest saveManifest keeps components the store gained", () => {
    afterEach(() => {
        vi.restoreAllMocks();
    });

    let useBuilderManifest: typeof import("./useBuilderManifest").useBuilderManifest;
    let mockArtifacts: Array<{ filename: string; mimeType: string; version?: number; accumulatedContent?: string }>;
    const mockUpload = vi.fn();
    const mockReadStored = vi.fn();

    beforeEach(async () => {
        vi.resetModules();
        mockUpload.mockReset().mockResolvedValue({ ok: true });
        mockReadStored.mockReset().mockResolvedValue(null);
        mockArtifacts = [];

        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({ data: mockArtifacts, isLoading: false, refetch: () => Promise.resolve() }),
        }));
        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => ({ downloadAndResolveArtifact: vi.fn().mockResolvedValue(null), sessionId: "session-1" }),
            api: { webui: { get: () => Promise.resolve([]), patch: () => Promise.resolve(undefined) } },
        }));
        vi.doMock("@/api/builder", () => ({
            useUploadManifestMutation: () => ({ mutateAsync: mockUpload }),
            useManifestContentQuery: (s: string | undefined, f: string, v: string, enabled?: boolean) => useQuery({ queryKey: ["manifestContent", s ?? "", f, v], queryFn: () => null, enabled: enabled !== false }),
            usePatchSessionNameMutation: () => ({ mutateAsync: vi.fn().mockResolvedValue(undefined) }),
            builderKeys: { manifestContent: (s: unknown, f: unknown, v: unknown) => ["manifestContent", s, f, v], manifests: () => ["manifests"] },
            getManifestContent: (...args: unknown[]) => mockReadStored(...args),
        }));

        useBuilderManifest = (await import("./useBuilderManifest")).useBuilderManifest;
    });

    const renderWithManifest = (components: Component[]) => {
        mockArtifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 0, accumulatedContent: manifestYaml(components) }];
        return renderHook(() => useBuilderManifest(), { wrapper: buildQueryWrapper() });
    };

    const save = async (h: ReturnType<typeof renderWithManifest>, updater: (prev: BuildManifest) => BuildManifest) => {
        let outcome;
        await act(async () => {
            outcome = await h.result.current.saveManifest(updater);
        });
        return outcome;
    };

    const heldComponents = [component("A"), component("B")];

    test.each([
        { name: "one component written since", stored: [...heldComponents, component("C")], expectedNames: ["A", "B", "C"] },
        { name: "several components written since", stored: [...heldComponents, component("C"), component("D"), component("E")], expectedNames: ["A", "B", "C", "D", "E"] },
        { name: "a component of another kind under a name the page holds", stored: [...heldComponents, component("A", "connector")], expectedNames: ["A", "B"] },
        { name: "a name that differs only in case", stored: [...heldComponents, component("a")], expectedNames: ["A", "B", "a"] },
        { name: "nothing written since", stored: heldComponents, expectedNames: ["A", "B"] },
        { name: "the store holds fewer components than the page", stored: [component("A")], expectedNames: ["A", "B"] },
        { name: "the store holds none", stored: [], expectedNames: ["A", "B"] },
    ])("a status flip over $name keeps $expectedNames", async ({ stored, expectedNames }) => {
        mockReadStored.mockResolvedValue(manifestYaml(stored));
        const h = renderWithManifest(heldComponents);

        const outcome = await save(h, markAllDeployed);

        expect(outcome).toEqual({ ok: true });
        expect(mockUpload).toHaveBeenCalledTimes(1);
        expect(uploadedComponents(mockUpload).map(c => c.name)).toEqual(expectedNames);
        expect(
            uploadedComponents(mockUpload)
                .slice(0, 2)
                .map(c => c.status)
        ).toEqual(["deployed", "deployed"]);
        expect(h.result.current.manifest?.components?.map(c => c.name)).toEqual(expectedNames);
    });

    test("a component the updater dropped stays dropped", async () => {
        mockReadStored.mockResolvedValue(manifestYaml(heldComponents));
        const h = renderWithManifest(heldComponents);

        await save(h, prev => ({ ...prev, components: (prev.components ?? []).filter(c => c.name !== "B") }));

        expect(uploadedComponents(mockUpload).map(c => c.name)).toEqual(["A"]);
    });

    test("a component the updater added and the store already holds is not duplicated", async () => {
        mockReadStored.mockResolvedValue(manifestYaml([...heldComponents, component("C")]));
        const h = renderWithManifest(heldComponents);

        await save(h, prev => ({ ...prev, components: [...(prev.components ?? []), component("C", "agent", "draft")] }));

        expect(uploadedComponents(mockUpload).map(c => `${c.name}:${c.status}`)).toEqual(["A:validated", "B:validated", "C:draft"]);
    });

    test("a component the updater renamed is not brought back under its old name", async () => {
        mockReadStored.mockResolvedValue(manifestYaml(heldComponents));
        const h = renderWithManifest(heldComponents);

        await save(h, prev => ({ ...prev, components: (prev.components ?? []).map(c => (c.name === "B" ? { ...c, name: "B2" } : c)) }));

        expect(uploadedComponents(mockUpload).map(c => c.name)).toEqual(["A", "B2"]);
    });

    test.each([
        { name: "the store read fails", arrange: () => mockReadStored.mockRejectedValue(new Error("offline")) },
        { name: "the store has nothing", arrange: () => mockReadStored.mockResolvedValue(null) },
    ])("the write still lands from the page's copy when $name", async ({ arrange }) => {
        arrange();
        const h = renderWithManifest(heldComponents);

        const outcome = await save(h, markAllDeployed);

        expect(outcome).toEqual({ ok: true });
        expect(uploadedComponents(mockUpload).map(c => c.name)).toEqual(["A", "B"]);
    });

    test("an absent store is reported once with a constant message", async () => {
        const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
        mockReadStored.mockResolvedValue(null);
        const h = renderWithManifest(heldComponents);

        await save(h, markAllDeployed);

        const absent = warn.mock.calls.filter(call => String(call[0]).includes("returned no manifest"));
        expect(absent).toEqual([["[builder] saveManifest: the store returned no manifest; writing from the page's copy", { sessionId: expect.any(String), filename: expect.any(String) }]]);
    });

    test("a component the page deleted stays deleted when the agent renamed it by id", async () => {
        mockReadStored.mockResolvedValue(manifestYaml([component("A"), component("B2", "agent", "validated", ID_B)]));
        const h = renderWithManifest([component("A"), component("B", "agent", "validated", ID_B)]);

        await save(h, prev => ({ ...prev, components: (prev.components ?? []).filter(c => c.name !== "B") }));

        expect(uploadedComponents(mockUpload).map(c => c.name)).toEqual(["A"]);
    });

    test("nothing is logged about skipped or kept components when the store gained nothing", async () => {
        const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
        mockReadStored.mockResolvedValue(manifestYaml(heldComponents));
        const h = renderWithManifest(heldComponents);

        await save(h, markAllDeployed);

        expect(warn.mock.calls.map(call => String(call[0])).filter(m => m.includes("not keeping") || m.includes("keeping components") || m.includes("returned no manifest"))).toHaveLength(0);
    });

    test("keeping what the store gained is logged once with the component names", async () => {
        const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
        mockReadStored.mockResolvedValue(manifestYaml([...heldComponents, component("C")]));
        const h = renderWithManifest(heldComponents);

        await save(h, markAllDeployed);

        const kept = warn.mock.calls.filter(call => String(call[0]).includes("keeping components written since this manifest was read"));
        expect(kept).toHaveLength(1);
        expect(JSON.stringify(kept[0][1])).toContain('"C"');
    });

    test("a failed store read is reported once with a constant message", async () => {
        const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
        mockReadStored.mockRejectedValue(new Error("https://secret.example/path?token=abc"));
        const h = renderWithManifest(heldComponents);

        await save(h, markAllDeployed);

        const messages = warn.mock.calls.map(call => String(call[0]));
        expect(messages.filter(m => m.includes("could not read the stored manifest before writing"))).toHaveLength(1);
        expect(messages.filter(m => m.includes("returned no manifest"))).toHaveLength(0);
        expect(JSON.stringify(warn.mock.calls)).not.toContain("secret.example");
    });

    test.each([
        { name: "the stored bytes do not parse", stored: "components: [unclosed" },
        { name: "the stored document is not a manifest", stored: "components: not-a-list\n" },
        { name: "a stored component has a null name", stored: "name: x\ncomponents:\n  - {filename: a.yaml, type: agent, name: null, status: validated}\n" },
        { name: "a stored component has no filename", stored: "name: x\ncomponents:\n  - {type: agent, name: A, status: validated}\n" },
    ])("the write is refused as unreadable and nothing is uploaded when $name", async ({ stored }) => {
        mockReadStored.mockResolvedValue(stored);
        const h = renderWithManifest(heldComponents);

        const outcome = await save(h, markAllDeployed);

        expect(outcome).toEqual({ ok: false, reason: "unreadable" });
        expect(mockUpload).not.toHaveBeenCalled();
    });

    test("a null in an optional field of the stored manifest reads as absent: the write lands and a stored component's null is not re-emitted", async () => {
        mockReadStored.mockResolvedValue(
            "name: x\nfull_validation_passed: null\ncomponents:\n  - {filename: A.yaml, type: agent, name: A, status: null, deployed_version: null}\n  - {filename: C.yaml, type: agent, name: C, status: validated, id: null}\n"
        );
        const h = renderWithManifest(heldComponents);

        const outcome = await save(h, markAllDeployed);

        expect(outcome).toEqual({ ok: true });
        expect(uploadedComponents(mockUpload).map(c => c.name)).toEqual(["A", "B", "C"]);
        expect(mockUpload.mock.calls[0][0].yamlText).not.toMatch(/null/);
    });

    test("the unreadable refusal is logged once however often the write is retried", async () => {
        const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
        mockReadStored.mockResolvedValue("components: [unclosed");
        const h = renderWithManifest(heldComponents);

        await save(h, markAllDeployed);
        await save(h, markAllDeployed);
        await save(h, markAllDeployed);

        const refusals = warn.mock.calls.filter(call => String(call[0]).includes("the latest stored manifest cannot be read"));
        expect(refusals).toHaveLength(1);
        expect(warn.mock.calls.filter(call => String(call[0]).includes("returned no manifest"))).toHaveLength(0);
    });

    test("an unreadable store is logged again when it turns unreadable after a good write", async () => {
        const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
        const h = renderWithManifest(heldComponents);

        mockReadStored.mockResolvedValueOnce("components: [unclosed");
        await save(h, markAllDeployed);
        mockReadStored.mockResolvedValueOnce(manifestYaml(heldComponents));
        await save(h, prev => ({ ...prev, components: (prev.components ?? []).map(c => ({ ...c, status: "error" })) }));
        mockReadStored.mockResolvedValueOnce("components: [unclosed");
        await save(h, markAllDeployed);

        const refusals = warn.mock.calls.filter(call => String(call[0]).includes("the latest stored manifest cannot be read"));
        expect(refusals).toHaveLength(2);
    });

    test("a component the agent renamed is not brought back beside its old name", async () => {
        mockReadStored.mockResolvedValue(manifestYaml([component("A"), component("B2", "agent", "validated", ID_B)]));
        const h = renderWithManifest([component("A"), component("B", "agent", "validated", ID_B)]);

        await save(h, markAllDeployed);

        expect(uploadedComponents(mockUpload).map(c => c.name)).toEqual(["A", "B"]);
    });

    test("a component the agent retyped is not brought back beside its old type", async () => {
        mockReadStored.mockResolvedValue(manifestYaml([component("A"), component("B", "workflow", "validated", ID_B)]));
        const h = renderWithManifest([component("A"), component("B", "agent", "validated", ID_B)]);

        await save(h, markAllDeployed);

        expect(uploadedComponents(mockUpload).map(c => `${c.type}:${c.name}`)).toEqual(["agent:A", "agent:B"]);
    });

    test("a stored component skipped because the page holds its name is logged with its name", async () => {
        const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
        mockReadStored.mockResolvedValue(manifestYaml([component("A"), component("B", "workflow"), component("C")]));
        const h = renderWithManifest([component("A"), component("B", "agent")]);

        await save(h, markAllDeployed);

        const skipped = warn.mock.calls.filter(call => String(call[0]).includes("not keeping stored components whose names the page already holds"));
        expect(skipped).toHaveLength(1);
        expect(JSON.stringify(skipped[0][1])).toContain('"B"');
        expect(JSON.stringify(skipped[0][1])).not.toContain('"C"');
    });

    test("an id-less retype is not duplicated beside the old type", async () => {
        mockReadStored.mockResolvedValue(manifestYaml([component("A"), component("B", "workflow")]));
        const h = renderWithManifest([component("A"), component("B", "agent")]);

        await save(h, markAllDeployed);

        expect(uploadedComponents(mockUpload).map(c => `${c.type}:${c.name}`)).toEqual(["agent:A", "agent:B"]);
    });

    test("an id-less rename looks like an addition and comes back beside the old name", async () => {
        mockReadStored.mockResolvedValue(manifestYaml([component("A"), component("B2")]));
        const h = renderWithManifest([component("A"), component("B")]);

        await save(h, markAllDeployed);

        expect(uploadedComponents(mockUpload).map(c => c.name)).toEqual(["A", "B", "B2"]);
    });

    test("a stored component under a held name is not an addition even with another id", async () => {
        mockReadStored.mockResolvedValue(manifestYaml([component("A"), component("B", "agent", "validated", "01a14000-0000-7000-8000-0000000000bb")]));
        const h = renderWithManifest([component("A"), component("B", "agent", "validated", ID_B)]);

        await save(h, markAllDeployed);

        expect(uploadedComponents(mockUpload).map(c => c.id ?? c.name)).toEqual(["A", ID_B]);
    });

    test("an update that changes nothing neither reads the store nor writes", async () => {
        mockReadStored.mockResolvedValue(manifestYaml([...heldComponents, component("C")]));
        const h = renderWithManifest(heldComponents);

        await save(h, prev => prev);

        expect(mockReadStored).not.toHaveBeenCalled();
        expect(mockUpload).not.toHaveBeenCalled();
    });

    test("reads the latest stored version of the session's manifest", async () => {
        const h = renderWithManifest(heldComponents);

        await save(h, markAllDeployed);

        expect(mockReadStored).toHaveBeenCalledWith("session-1", "build_manifest.yaml", "latest");
    });

    test("a failed upload still reports failure after the store was read", async () => {
        mockReadStored.mockResolvedValue(manifestYaml([...heldComponents, component("C")]));
        mockUpload.mockResolvedValue({ ok: false });
        const h = renderWithManifest(heldComponents);

        const outcome = await save(h, markAllDeployed);

        expect(outcome).toEqual({ ok: false, reason: "failed" });
    });

    test("a second queued write builds on what the first kept, even when the store cannot be read again", async () => {
        mockReadStored.mockResolvedValueOnce(manifestYaml([...heldComponents, component("C")])).mockRejectedValueOnce(new Error("offline"));
        const h = renderWithManifest(heldComponents);

        await act(async () => {
            await Promise.all([
                h.result.current.saveManifest(prev => ({ ...prev, components: (prev.components ?? []).map(c => (c.name === "A" ? { ...c, status: "deployed" } : c)) })),
                h.result.current.saveManifest(prev => ({ ...prev, components: (prev.components ?? []).map(c => (c.name === "B" ? { ...c, status: "deployed" } : c)) })),
            ]);
        });

        expect(mockUpload).toHaveBeenCalledTimes(2);
        const last = yaml.load(mockUpload.mock.calls[1][0].yamlText) as { components: Array<{ name: string; status: string }> };
        expect(last.components.map(c => `${c.name}:${c.status}`)).toEqual(["A:deployed", "B:deployed", "C:validated"]);
    });
});

describe("component identity contract shared with the Builder's merge tools", () => {
    const vectors = JSON.parse(readFileSync(resolve(process.cwd(), "..", "pkg", "tools", "testdata", "manifest_component_identity.json"), "utf8")) as Array<{
        name: string;
        a: Record<string, unknown>;
        b: Record<string, unknown>;
        same: boolean;
    }>;
    const asComponent = (fields: Record<string, unknown>) => ({ filename: "x.yaml", ...fields }) as Parameters<typeof isSameComponent>[0];

    test("the vectors hold the floor the Go side holds", () => {
        expect(vectors.length).toBeGreaterThanOrEqual(22);
    });

    test.each(vectors)("$name", ({ a, b, same }) => {
        expect(isSameComponent(asComponent(a), asComponent(b))).toBe(same);
        expect(isSameComponent(asComponent(b), asComponent(a))).toBe(same);
    });
});
