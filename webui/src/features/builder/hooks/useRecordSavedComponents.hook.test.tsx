// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { builderKeys } from "@/api/builder";

import type { BuildManifest, SaveManifestResult } from "./useBuilderManifest";
import { type SaveManifest, type SaveProof, useRecordSavedComponents, type VersionsByFile } from "./useRecordSavedComponents";

const listManifestVersions = vi.fn();

vi.mock("@/providers/contexts", async importOriginal => ({ ...(await importOriginal<object>()), useChatContext: () => ({ sessionId: "session-1" }) }));
vi.mock("@/api/builder", async importOriginal => ({ ...(await importOriginal<object>()), listManifestVersions: (...args: unknown[]) => listManifestVersions(...args) }));

type Comp = NonNullable<BuildManifest["components"]>[number];

const comp = (name: string, type: string): Comp => ({ filename: `${name}.yaml`, type, name, status: "validated", origin: "session" }) as Comp;
const manifest: BuildManifest = { name: "b", components: [comp("db", "connector"), comp("Agent", "agent"), comp("notes", "skill")] };
const proof = (extra: Partial<SaveProof> = {}): SaveProof => ({ changed: [], matched: [], declined: [], ...extra });
const readAt = (...entries: Array<[string, number]>): VersionsByFile => new Map(entries);

const wrapper = (client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) => {
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
};

const stampedBy = (saveManifest: ReturnType<typeof vi.fn>) => (saveManifest.mock.calls[0][0] as (prev: BuildManifest) => BuildManifest)(manifest).components;

describe("useRecordSavedComponents", () => {
    const ok: SaveManifestResult = { ok: true };
    let saveManifest: ReturnType<typeof vi.fn<SaveManifest>>;
    let warn: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        listManifestVersions.mockReset().mockResolvedValue([0, 1, 2]);
        saveManifest = vi.fn<SaveManifest>().mockResolvedValue(ok);
        warn = vi.spyOn(console, "warn").mockImplementation(() => {});
        warn.mockClear();
    });

    const recorder = (save: SaveManifest | null = saveManifest) => renderHook(() => useRecordSavedComponents(save ?? undefined), { wrapper: wrapper() }).result.current;

    describe("versionsBeforeSave", () => {
        test("reads the newest version of every connector and skill config, and of nothing else", async () => {
            listManifestVersions.mockImplementation((_session: string, filename: string) => Promise.resolve(filename === "notes.yaml" ? [0, 1, 2, 3, 4] : [0, 1]));

            const versions = await recorder().versionsBeforeSave(manifest.components!);

            expect(versions).toEqual({
                at: new Map([
                    ["db.yaml", 1],
                    ["notes.yaml", 4],
                ]),
                failed: [],
            });
            expect(listManifestVersions.mock.calls.map(([, filename]) => filename).sort()).toEqual(["db.yaml", "notes.yaml"]);
        });

        test("a listing cached earlier does not win over a fresh one", async () => {
            const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
            client.setQueryData(builderKeys.manifestVersions("session-1", "db.yaml"), [0]);
            const { versionsBeforeSave } = renderHook(() => useRecordSavedComponents(saveManifest), { wrapper: wrapper(client) }).result.current;

            expect((await versionsBeforeSave(manifest.components!)).at.get("db.yaml")).toBe(2);
        });

        test("names every config whose lookup failed, and does not throw", async () => {
            listManifestVersions.mockRejectedValue(new Error("boom"));

            await expect(recorder().versionsBeforeSave(manifest.components!)).resolves.toEqual({ at: new Map(), failed: ["db.yaml", "notes.yaml"] });
        });

        test("names only the config whose lookup failed", async () => {
            listManifestVersions.mockImplementation((_session: string, filename: string) => (filename === "notes.yaml" ? Promise.reject(new Error("boom")) : Promise.resolve([0, 1])));

            await expect(recorder().versionsBeforeSave(manifest.components!)).resolves.toEqual({ at: new Map([["db.yaml", 1]]), failed: ["notes.yaml"] });
        });

        test("a config the store holds no version of is neither read nor failed: nothing to pin, and no outage", async () => {
            listManifestVersions.mockResolvedValue([]);

            await expect(recorder().versionsBeforeSave(manifest.components!)).resolves.toEqual({ at: new Map(), failed: [] });
        });

        test("reads the files of session connectors and skills only, each once, and not a platform component's", async () => {
            const platformOne = { ...comp("shared", "connector"), origin: "platform" } as Comp;
            const twice = comp("db", "connector");
            const components = [comp("db", "connector"), twice, { ...twice, name: "db-again" } as Comp, platformOne, { name: "nofile", type: "skill", status: "validated", origin: "session" } as Comp];

            await expect(recorder().versionsBeforeSave(components)).resolves.toEqual({ at: new Map([["db.yaml", 2]]), failed: [] });
            expect(listManifestVersions.mock.calls.map(([, filename]) => filename)).toEqual(["db.yaml"]);
        });

        test("a lookup refused with 404 is a failed lookup, not a file the store has no version of", async () => {
            listManifestVersions.mockRejectedValue(Object.assign(new Error("not found"), { status: 404 }));

            await expect(recorder().versionsBeforeSave([comp("db", "connector")])).resolves.toEqual({ at: new Map(), failed: ["db.yaml"] });
        });

        test("an answer that is not a list of versions is a failed lookup, and a non-integer entry is no version", async () => {
            listManifestVersions.mockImplementation((_session: string, filename: string) => Promise.resolve(filename === "db.yaml" ? { version: 2 } : ["x", 2, { version: 9 }]));

            await expect(recorder().versionsBeforeSave([comp("db", "connector"), comp("notes", "skill")])).resolves.toEqual({ at: new Map([["notes.yaml", 2]]), failed: ["db.yaml"] });
        });

        test("logs a file with no versions and a failed lookup once each, with the filename and no payload", async () => {
            listManifestVersions.mockImplementation(async (_session: string, filename: string) => {
                if (filename === "notes.yaml") throw Object.assign(new Error("boom"), { status: 503 });
                return [];
            });

            await recorder().versionsBeforeSave([comp("db", "connector"), comp("notes", "skill")]);

            expect(warn.mock.calls).toEqual([
                ["[builder] a config has no versions in the store", { filename: "db.yaml" }],
                ["[builder] could not read the versions a save is about to send", { filename: "notes.yaml", error: "boom" }],
            ]);
        });

        test("reads nothing for a build that has no connector or skill", async () => {
            const agentOnly: BuildManifest = { name: "b", components: [comp("Agent", "agent")] };
            const { versionsBeforeSave } = renderHook(() => useRecordSavedComponents(saveManifest), { wrapper: wrapper() }).result.current;

            await expect(versionsBeforeSave(agentOnly.components!)).resolves.toEqual({ at: new Map(), failed: [] });
            expect(listManifestVersions).not.toHaveBeenCalled();
        });
    });

    describe("record", () => {
        test("stamps a saved connector with the version the save was read at, not a newer one", async () => {
            const recorded = await recorder().record(proof({ changed: ["db"] }), readAt(["db.yaml", 1]), manifest.components!);

            expect(recorded).toEqual(new Map([["db.yaml", 1]]));
            expect(listManifestVersions).not.toHaveBeenCalled();
            expect(saveManifest).toHaveBeenCalledTimes(1);
            expect(saveManifest.mock.calls[0][1]).toEqual({ forSession: "session-1" });
            expect(stampedBy(saveManifest)?.map(c => [c.name, c.deployed_version])).toEqual([
                ["db", 1],
                ["Agent", undefined],
                ["notes", undefined],
            ]);
        });

        test("stamps a saved skill the same way, and a connector beside it with its own version", async () => {
            const recorded = await recorder().record(proof({ changed: ["notes"], matched: ["db"] }), readAt(["notes.yaml", 4], ["db.yaml", 1]), manifest.components!);

            expect(recorded).toEqual(
                new Map([
                    ["db.yaml", 1],
                    ["notes.yaml", 4],
                ])
            );
            expect(stampedBy(saveManifest)?.map(c => [c.name, c.deployed_version])).toEqual([
                ["db", 1],
                ["Agent", undefined],
                ["notes", 4],
            ]);
        });

        test.each([
            ["nothing was saved", proof(), readAt(["db.yaml", 1])],
            ["only an agent was saved", proof({ changed: ["Agent"] }), readAt(["db.yaml", 1])],
            ["the connector's edit was partly declined", proof({ changed: ["db"], declined: ["db"] }), readAt(["db.yaml", 1])],
            ["the version the connector was read at is not known", proof({ changed: ["db"] }), readAt()],
        ])("writes nothing, and says nothing was recorded, when %s", async (_label, p, read) => {
            await expect(recorder().record(p, read, manifest.components!)).resolves.toEqual(new Map());

            expect(saveManifest).not.toHaveBeenCalled();
            expect(warn).not.toHaveBeenCalled();
        });

        test("records the components whose version is known and leaves out one whose is not", async () => {
            const recorded = await recorder().record(proof({ changed: ["db", "notes"] }), readAt(["notes.yaml", 4]), manifest.components!);

            expect(recorded).toEqual(new Map([["notes.yaml", 4]]));
            expect(stampedBy(saveManifest)?.map(c => [c.name, c.deployed_version])).toEqual([
                ["db", undefined],
                ["Agent", undefined],
                ["notes", 4],
            ]);
        });

        test("does nothing, without throwing, when no manifest writer is available", async () => {
            await expect(recorder(null).record(proof({ changed: ["db"] }), readAt(["db.yaml", 1]), manifest.components!)).resolves.toEqual(new Map());
            expect(warn).toHaveBeenCalledWith(expect.stringContaining("not recorded"), expect.objectContaining({ names: ["db"] }));
        });

        test.each([
            ["the manifest write is refused", () => saveManifest.mockResolvedValue({ ok: false, reason: "unreadable" }), new Map([["db.yaml", 1]])],
            ["the manifest write throws", () => saveManifest.mockRejectedValue(new Error("boom")), new Map([["db.yaml", 1]])],
        ])("does not throw when %s", async (_label, arrange, expected) => {
            arrange();

            await expect(recorder().record(proof({ changed: ["db"] }), readAt(["db.yaml", 1]), manifest.components!)).resolves.toEqual(expected);

            expect(saveManifest).toHaveBeenCalledTimes(1);
        });

        test("does not throw on a component whose name is not a string", async () => {
            const odd = { name: "b", components: [{ ...comp("db", "connector"), name: 123 as unknown as string }] } as BuildManifest;
            const { record } = renderHook(() => useRecordSavedComponents(saveManifest), { wrapper: wrapper() }).result.current;

            await expect(record(proof({ changed: ["db"] }), readAt(["db.yaml", 1]), odd.components!)).resolves.toEqual(new Map());

            expect(saveManifest).not.toHaveBeenCalled();
        });

        test("records against the components it is given, not against whatever the manifest has become", async () => {
            const repointed: BuildManifest = { name: "b", components: [{ ...comp("db", "connector"), filename: "db_v2.yaml" }] };
            const { record } = renderHook(() => useRecordSavedComponents(saveManifest), { wrapper: wrapper() }).result.current;

            await record(proof({ changed: ["db"] }), readAt(["db.yaml", 1]), manifest.components!);

            const stamped = (saveManifest.mock.calls[0][0] as (prev: BuildManifest) => BuildManifest)(repointed).components?.map(c => [c.name, c.deployed_version]);
            expect(stamped).toEqual([["db", undefined]]);
        });
    });
});
