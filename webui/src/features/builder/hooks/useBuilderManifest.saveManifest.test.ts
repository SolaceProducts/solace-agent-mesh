// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { type BuildManifest, SAM_BUILD_MANIFEST_MIME_TYPE, type SaveManifestResult } from "./useBuilderManifest";

function buildQueryWrapper() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

describe("useBuilderManifest saveManifest", () => {
    let useBuilderManifest: typeof import("./useBuilderManifest").useBuilderManifest;

    type Artifact = { filename: string; mimeType: string; version?: number; accumulatedContent?: string; creationStatus?: "in-progress" | "completed" | "failed" };
    let mockArtifacts: Artifact[];
    let mockLatestContent: string | undefined;
    let mockSessionId: string;
    const mockUpload = vi.fn();

    beforeEach(async () => {
        vi.resetModules();
        mockArtifacts = [];
        mockLatestContent = undefined;
        mockSessionId = "session-1";
        mockUpload.mockReset().mockResolvedValue({ ok: true });
        const downloadAndResolveArtifact = vi.fn().mockResolvedValue(null);

        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({ data: mockArtifacts, isLoading: false, refetch: () => Promise.resolve() }),
        }));
        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => ({ downloadAndResolveArtifact, sessionId: mockSessionId }),
            api: { webui: { get: () => Promise.resolve([]), patch: () => Promise.resolve(undefined) } },
        }));
        vi.doMock("@/api/builder", () => ({
            useUploadManifestMutation: () => ({ mutateAsync: mockUpload }),
            useManifestContentQuery: (s: string | undefined, f: string, v: string, enabled?: boolean) => useQuery({ queryKey: ["manifestContent", s ?? "", f, v], queryFn: () => mockLatestContent ?? null, enabled: enabled !== false }),
            usePatchSessionNameMutation: () => ({ mutateAsync: vi.fn().mockResolvedValue(undefined) }),
            builderKeys: { manifestContent: (s: unknown, f: unknown, v: unknown) => ["manifestContent", s, f, v], manifests: () => ["manifests"] },
            getManifestContent: vi.fn().mockResolvedValue(null),
        }));

        const mod = await import("./useBuilderManifest");
        useBuilderManifest = mod.useBuilderManifest;
    });

    const SEEDED = "name: my-build\ncomponents:\n  - filename: agent.yaml\n    type: agent\n    name: Foo\n    status: validated\n";

    const rerenderRef: { current: (() => void) | null } = { current: null };

    const withSeededManifest = () => {
        mockArtifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 0, accumulatedContent: SEEDED }];
        const h = renderHook(() => useBuilderManifest(), { wrapper: buildQueryWrapper() });
        rerenderRef.current = () => h.rerender();
        return h;
    };

    test("does not upload when the update leaves the manifest unchanged", async () => {
        const { result } = withSeededManifest();
        expect(result.current.manifest?.name).toBe("my-build");

        let ok: SaveManifestResult | undefined;
        await act(async () => {
            ok = await result.current.saveManifest(prev => prev);
        });

        expect(mockUpload).not.toHaveBeenCalled();
        expect(ok).toEqual({ ok: true });
    });

    test("does not upload when an updater rebuilds an identical manifest", async () => {
        const { result } = withSeededManifest();

        await act(async () => {
            await result.current.saveManifest(prev => ({ ...prev, components: (prev.components ?? []).map(c => ({ ...c })) }));
        });

        expect(mockUpload).not.toHaveBeenCalled();
    });

    test("still uploads when the update actually changes something", async () => {
        const { result } = withSeededManifest();

        await act(async () => {
            await result.current.saveManifest(prev => ({
                ...prev,
                components: (prev.components ?? []).map(c => ({ ...c, status: "deployed" })),
            }));
        });

        expect(mockUpload).toHaveBeenCalledTimes(1);
        expect(mockUpload.mock.calls[0][0].yamlText).toContain("deployed");
    });

    test("falls back to build_manifest.yaml when the manifest is excluded from the artifact list", async () => {
        const yamlText = "name: my-build\ncomponents:\n  - filename: agent.yaml\n    type: agent\n    name: Foo\n    status: validated\n";
        mockArtifacts = [{ filename: "weird-name.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 0, accumulatedContent: yamlText }];
        const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: buildQueryWrapper() });
        expect(result.current.manifest?.name).toBe("my-build");

        mockArtifacts = [];
        rerender();

        let ok: SaveManifestResult = { ok: false, reason: "failed" };
        await act(async () => {
            ok = await result.current.saveManifest(prev => ({ ...prev, components: prev.components?.map(c => ({ ...c, status: "deployed" })) }));
        });

        expect(ok).toEqual({ ok: true });
        expect(mockUpload).toHaveBeenCalledTimes(1);
        expect(mockUpload.mock.calls[0][0]).toMatchObject({ sessionId: "session-1", filename: "build_manifest.yaml" });
    });

    describe("when the stored manifest cannot be read", () => {
        const BROKEN = "components:\n  - filename: agent.yaml\n    type: agent\n    name: Foo\n";

        const withBrokenAfterGood = () => {
            mockArtifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 0, accumulatedContent: SEEDED }];
            const h = renderHook(() => useBuilderManifest(), { wrapper: buildQueryWrapper() });
            mockArtifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 1, accumulatedContent: BROKEN }];
            h.rerender();
            return h;
        };

        test("refuses instead of uploading the cached manifest over it", async () => {
            const { result } = withBrokenAfterGood();

            let ok: SaveManifestResult | undefined;
            await act(async () => {
                ok = await result.current.saveManifest(prev => ({ ...prev, name: "renamed" }));
            });

            expect(mockUpload).not.toHaveBeenCalled();
            expect(ok).toEqual({ ok: false, reason: "unreadable" });
        });

        test("still saves when there are no bytes at all, only a cached manifest", async () => {
            mockArtifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 0, accumulatedContent: SEEDED }];
            const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: buildQueryWrapper() });
            expect(result.current.manifest?.name).toBe("my-build");

            mockArtifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 1 }];
            rerender();
            await waitFor(() => expect(result.current.isUnreadable).toBe(false));

            let ok: SaveManifestResult | undefined;
            await act(async () => {
                ok = await result.current.saveManifest(prev => ({ ...prev, name: "renamed" }));
            });

            expect(ok).toEqual({ ok: true });
            expect(mockUpload).toHaveBeenCalledTimes(1);
        });

        test("refuses through a callback captured before the bytes went bad", async () => {
            mockArtifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 0, accumulatedContent: SEEDED }];
            const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: buildQueryWrapper() });
            expect(result.current.manifest?.name).toBe("my-build");
            const inFlight = result.current.saveManifest;

            mockArtifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 1, accumulatedContent: BROKEN }];
            rerender();
            await waitFor(() => expect(result.current.isUnreadable).toBe(true));

            let ok: SaveManifestResult | undefined;
            await act(async () => {
                ok = await inFlight(prev => ({ ...prev, name: "renamed" }));
            });

            expect(ok).toEqual({ ok: false, reason: "unreadable" });
            expect(mockUpload).not.toHaveBeenCalled();
        });

        test("still saves during a rewrite, where the cached manifest is the intended target", async () => {
            mockArtifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 0, accumulatedContent: SEEDED, creationStatus: "completed" }];
            const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: buildQueryWrapper() });
            expect(result.current.manifest?.name).toBe("my-build");

            mockArtifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 1, accumulatedContent: BROKEN, creationStatus: "in-progress" }];
            rerender();

            let ok: SaveManifestResult | undefined;
            await act(async () => {
                ok = await result.current.saveManifest(prev => ({ ...prev, name: "renamed" }));
            });

            expect(ok).toEqual({ ok: true });
            expect(mockUpload).toHaveBeenCalledTimes(1);
        });

        test("still saves normally once the manifest parses again", async () => {
            const { result, rerender } = withBrokenAfterGood();

            mockArtifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 2, accumulatedContent: SEEDED }];
            rerender();

            let ok: SaveManifestResult | undefined;
            await act(async () => {
                ok = await result.current.saveManifest(prev => ({ ...prev, name: "renamed" }));
            });

            expect(ok).toEqual({ ok: true });
            expect(mockUpload).toHaveBeenCalledTimes(1);
            expect(String((mockUpload.mock.calls[0][0] as { yamlText: string }).yamlText)).toContain("renamed");
        });
    });

    describe("when the streamed artifact content sits behind a local write", () => {
        const flipToDeployed = (prev: BuildManifest): BuildManifest => ({
            ...prev,
            components: prev.components?.map(c => (c.status === "validated" ? { ...c, status: "deployed" } : c)),
        });

        test("keeps the saved manifest instead of reverting to the streamed snapshot", async () => {
            const { result } = withSeededManifest();

            await act(async () => {
                await result.current.saveManifest(flipToDeployed);
            });

            expect(mockUpload).toHaveBeenCalledTimes(1);
            expect(result.current.manifest?.components?.[0].status).toBe("deployed");
        });

        test("does not upload a document identical to the one it last wrote", async () => {
            const { result } = withSeededManifest();

            await act(async () => {
                await result.current.saveManifest(flipToDeployed);
            });
            await act(async () => {
                await result.current.saveManifest(flipToDeployed);
            });

            expect(mockUpload).toHaveBeenCalledTimes(1);
        });

        test("writes again once a different document has been observed", async () => {
            const { result } = withSeededManifest();

            await act(async () => {
                await result.current.saveManifest(flipToDeployed);
            });
            expect(mockUpload).toHaveBeenCalledTimes(1);

            const agentRewrite = "components:\n    - status: validated\n      name: Foo\n      type: agent\n      filename: agent.yaml\nname: my-build\n";
            mockArtifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 1, accumulatedContent: agentRewrite }];
            rerenderRef.current?.();
            await waitFor(() => expect(result.current.manifest?.components?.[0].status).toBe("validated"));

            await act(async () => {
                await result.current.saveManifest(flipToDeployed);
            });

            expect(mockUpload).toHaveBeenCalledTimes(2);
        });

        test("still writes when the update carries evidence the last write lacked", async () => {
            const { result } = withSeededManifest();

            await act(async () => {
                await result.current.saveManifest(flipToDeployed);
            });
            await act(async () => {
                await result.current.saveManifest(prev => ({
                    ...prev,
                    components: prev.components?.map(c => ({ ...c, status: "deployed", id: "wf-1", deployed_version: 0 })),
                }));
            });

            expect(mockUpload).toHaveBeenCalledTimes(2);
            expect(mockUpload.mock.calls[1][0].yamlText).toContain("wf-1");
        });

        test("keeps the saved manifest when the content query still holds pre-write bytes", async () => {
            mockLatestContent = SEEDED;
            mockArtifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 0 }];
            const { result } = renderHook(() => useBuilderManifest(), { wrapper: buildQueryWrapper() });
            await waitFor(() => expect(result.current.manifest?.name).toBe("my-build"));

            await act(async () => {
                await result.current.saveManifest(flipToDeployed);
            });

            await waitFor(() => expect(result.current.manifest?.components?.[0].status).toBe("deployed"));
            expect(mockUpload).toHaveBeenCalledTimes(1);
        });
    });

    describe("writers that do not share a render", () => {
        const addField =
            (field: string, value: number) =>
            (prev: BuildManifest): BuildManifest => ({ ...prev, components: prev.components?.map(c => ({ ...c, [field]: value })) });
        const lastUploadedYaml = () => mockUpload.mock.calls[mockUpload.mock.calls.length - 1][0].yamlText as string;

        test("a write through a stale function still builds on the one that landed in between", async () => {
            const { result } = withSeededManifest();
            const stale = result.current.saveManifest;

            await act(async () => {
                await result.current.saveManifest(addField("first_field", 1));
            });
            await act(async () => {
                await stale(addField("second_field", 2));
            });

            expect(mockUpload).toHaveBeenCalledTimes(2);
            expect(lastUploadedYaml()).toContain("first_field: 1");
            expect(lastUploadedYaml()).toContain("second_field: 2");
        });

        test("a stale function does not fall back on the manifest parsed before the last write", async () => {
            mockLatestContent = SEEDED;
            mockArtifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 0 }];
            const { result } = renderHook(() => useBuilderManifest(), { wrapper: buildQueryWrapper() });
            await waitFor(() => expect(result.current.manifest?.name).toBe("my-build"));
            const stale = result.current.saveManifest;

            await act(async () => {
                await result.current.saveManifest(addField("first_field", 1));
            });
            await act(async () => {
                await stale(addField("second_field", 2));
            });

            expect(lastUploadedYaml()).toContain("first_field: 1");
            expect(lastUploadedYaml()).toContain("second_field: 2");
        });

        const heldUpload = () => {
            let release: (result: { ok: boolean }) => void = () => {};
            mockUpload.mockImplementationOnce(() => new Promise<{ ok: boolean }>(resolve => (release = resolve)));
            return (result: { ok: boolean } = { ok: true }) => release(result);
        };
        const seededByQuery = async () => {
            mockLatestContent = SEEDED;
            mockArtifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 0 }];
            const hook = renderHook(() => useBuilderManifest(), { wrapper: buildQueryWrapper() });
            await waitFor(() => expect(hook.result.current.manifest?.name).toBe("my-build"));
            return hook;
        };

        test("a write issued while the first upload is pending lands after it, one upload each", async () => {
            const { result } = await seededByQuery();
            const release = heldUpload();

            let first: Promise<SaveManifestResult> | undefined;
            let second: Promise<SaveManifestResult> | undefined;
            await act(async () => {
                first = result.current.saveManifest(addField("first_field", 1));
                second = result.current.saveManifest(addField("second_field", 2));
            });
            expect(mockUpload).toHaveBeenCalledTimes(1);

            await act(async () => {
                release();
                await Promise.all([first, second]);
            });

            expect(mockUpload).toHaveBeenCalledTimes(2);
            expect(mockUpload.mock.calls[0][0].yamlText).not.toContain("second_field");
            expect(lastUploadedYaml()).toContain("first_field: 1");
            expect(lastUploadedYaml()).toContain("second_field: 2");
        });

        test("a write that fails does not stop the next one, which builds on what was last saved", async () => {
            const { result } = await seededByQuery();
            const release = heldUpload();

            let outcomes: Promise<SaveManifestResult[]> | undefined;
            await act(async () => {
                outcomes = Promise.all([result.current.saveManifest(addField("first_field", 1)), result.current.saveManifest(addField("second_field", 2))]);
            });
            await act(async () => {
                release({ ok: false });
                await outcomes;
            });

            expect(await outcomes).toEqual([{ ok: false, reason: "failed" }, { ok: true }]);
            expect(lastUploadedYaml()).toContain("second_field: 2");
            expect(lastUploadedYaml()).not.toContain("first_field");
        });

        describe("a session switch", () => {
            const SESSION_CHANGED: SaveManifestResult = { ok: false, reason: "session_changed" };
            const OTHER_MANIFEST = "name: other-build\ncomponents:\n  - filename: other.yaml\n    type: agent\n    name: Other\n    status: validated\n";
            const uploadedSessions = () => mockUpload.mock.calls.map(c => c[0].sessionId);

            const switchTo = (sessionId: string, rerender: () => void, content?: string) => {
                mockSessionId = sessionId;
                mockLatestContent = content;
                rerender();
            };

            test("queued behind an upload that is pending when the session changes: dropped, reported as such", async () => {
                const { result, rerender } = await seededByQuery();
                const release = heldUpload();
                let second: Promise<SaveManifestResult> | undefined;
                await act(async () => {
                    void result.current.saveManifest(addField("first_field", 1));
                    second = result.current.saveManifest(addField("second_field", 2));
                });

                switchTo("session-2", rerender, OTHER_MANIFEST);
                await act(async () => {
                    release();
                    await second;
                });

                expect(await second).toEqual(SESSION_CHANGED);
                expect(uploadedSessions()).toEqual(["session-1"]);
            });

            test("no switch: the queued write lands", async () => {
                const { result } = await seededByQuery();
                const release = heldUpload();
                let second: Promise<SaveManifestResult> | undefined;
                await act(async () => {
                    void result.current.saveManifest(addField("first_field", 1));
                    second = result.current.saveManifest(addField("second_field", 2));
                });

                await act(async () => {
                    release();
                    await second;
                });

                expect(await second).toEqual({ ok: true });
                expect(uploadedSessions()).toEqual(["session-1", "session-1"]);
            });

            test("away and back before its turn: the write lands in the session it was for", async () => {
                const { result, rerender } = await seededByQuery();
                const release = heldUpload();
                let second: Promise<SaveManifestResult> | undefined;
                await act(async () => {
                    void result.current.saveManifest(addField("first_field", 1));
                    second = result.current.saveManifest(addField("second_field", 2));
                });

                switchTo("session-2", rerender, OTHER_MANIFEST);
                switchTo("session-1", rerender, SEEDED);
                await act(async () => {
                    release();
                    await second;
                });

                expect(await second).toEqual({ ok: true });
                expect(uploadedSessions()).toEqual(["session-1", "session-1"]);
            });

            test.each([
                ["a session with its own manifest", OTHER_MANIFEST, "other-build"],
                ["a fresh session with none", undefined, undefined],
            ])("an upload executing at the switch does not re-seed %s", async (_label, content, expectedName) => {
                const { result, rerender } = await seededByQuery();
                const release = heldUpload();
                let first: Promise<SaveManifestResult> | undefined;
                await act(async () => {
                    first = result.current.saveManifest(addField("first_field", 1));
                });

                switchTo("session-2", rerender, content);
                if (expectedName) await waitFor(() => expect(result.current.manifest?.name).toBe(expectedName));
                await act(async () => {
                    release();
                    await first;
                });

                expect(await first).toEqual({ ok: true });
                expect(result.current.manifest?.name).toBe(expectedName);
                if (expectedName) {
                    await act(async () => {
                        await result.current.saveManifest(addField("later_field", 3));
                    });
                    expect(uploadedSessions()).toEqual(["session-1", "session-2"]);
                    expect(lastUploadedYaml()).toContain("name: other-build");
                    expect(lastUploadedYaml()).toContain("later_field: 3");
                    expect(lastUploadedYaml()).not.toContain("first_field");
                }
            });

            test.each([
                ["still in the session it started in", "session-1", { ok: true }, ["session-1"]],
                ["moved to another session since it started", "session-0", SESSION_CHANGED, []],
            ])("a write that names its session: %s", async (_label, forSession, expected, sessions) => {
                const { result } = await seededByQuery();

                let outcome: SaveManifestResult | undefined;
                await act(async () => {
                    outcome = await result.current.saveManifest(addField("late_field", 1), { forSession });
                });

                expect(outcome).toEqual(expected);
                expect(uploadedSessions()).toEqual(sessions);
            });
        });

        test("two writes in flight at once both survive", async () => {
            mockLatestContent = SEEDED;
            mockArtifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 0 }];
            const { result } = renderHook(() => useBuilderManifest(), { wrapper: buildQueryWrapper() });
            await waitFor(() => expect(result.current.manifest?.name).toBe("my-build"));
            const { saveManifest } = result.current;

            await act(async () => {
                await Promise.all([saveManifest(addField("first_field", 1)), saveManifest(addField("second_field", 2))]);
            });

            expect(lastUploadedYaml()).toContain("first_field: 1");
            expect(lastUploadedYaml()).toContain("second_field: 2");
        });
    });
});
