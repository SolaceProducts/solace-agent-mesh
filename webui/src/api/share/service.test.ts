// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { beforeEach, describe, expect, test, vi } from "vitest";

import type * as ServiceModule from "@/api/share/service";

const mockGet = vi.fn();
const mockPost = vi.fn();
const mockPatch = vi.fn();
const mockDelete = vi.fn();

describe("share service", () => {
    let service: typeof ServiceModule;

    beforeEach(async () => {
        vi.resetModules();
        mockGet.mockReset();
        mockPost.mockReset();
        mockPatch.mockReset();
        mockDelete.mockReset();

        vi.doMock("@/api/client", () => ({
            api: {
                webui: {
                    get: mockGet,
                    post: mockPost,
                    patch: mockPatch,
                    delete: mockDelete,
                },
            },
        }));

        service = await import("@/api/share/service");
    });

    describe("getShareLinkForSession", () => {
        test("returns camelCased share link when found", async () => {
            const fakeResponse = {
                ok: true,
                status: 200,
                json: vi.fn().mockResolvedValue({
                    id: "s1",
                    sessionId: "sess1",
                    createdAt: "2025-01-01",
                }),
            };
            mockGet.mockResolvedValue(fakeResponse);

            const result = await service.getShareLinkForSession("sess1");

            expect(mockGet).toHaveBeenCalledWith("/api/v1/share/link/sess1", { fullResponse: true });
            expect(result).toEqual({
                id: "s1",
                sessionId: "sess1",
                createdAt: "2025-01-01",
            });
        });

        test("returns null when server responds with 404", async () => {
            const fakeResponse = { ok: false, status: 404 };
            mockGet.mockResolvedValue(fakeResponse);

            const result = await service.getShareLinkForSession("not-found");

            expect(result).toBeNull();
        });

        test("throws when server responds with non-404 error", async () => {
            const fakeResponse = {
                ok: false,
                status: 500,
                json: vi.fn().mockResolvedValue({ message: "Server error" }),
            };
            mockGet.mockResolvedValue(fakeResponse);

            await expect(service.getShareLinkForSession("sess1")).rejects.toThrow("Server error");
        });

        test("throws with default message when error json parsing fails", async () => {
            const fakeResponse = {
                ok: false,
                status: 500,
                json: vi.fn().mockRejectedValue(new Error("parse error")),
            };
            mockGet.mockResolvedValue(fakeResponse);

            await expect(service.getShareLinkForSession("sess1")).rejects.toThrow("Failed to get share link");
        });
    });

    describe("getShareUsers", () => {
        test("calls GET with correct URL and returns camelCased response", async () => {
            mockGet.mockResolvedValue({ users: [{ userEmail: "a@b.com", accessLevel: "read" }] });

            const result = await service.getShareUsers("share-1");

            expect(mockGet).toHaveBeenCalledWith("/api/v1/share/share-1/users");
            expect(result).toEqual({ users: [{ userEmail: "a@b.com", accessLevel: "read" }] });
        });
    });

    describe("createShareLink", () => {
        test("calls POST and returns camelCased share link", async () => {
            mockPost.mockResolvedValue({ id: "new-share", sessionId: "sess2" });

            const result = await service.createShareLink("sess2", { requireAuthentication: true });

            expect(mockPost).toHaveBeenCalledWith("/api/v1/share/sess2", { requireAuthentication: true });
            expect(result).toEqual({ id: "new-share", sessionId: "sess2" });
        });

        test("uses empty options object by default", async () => {
            mockPost.mockResolvedValue({ id: "s" });

            await service.createShareLink("sess3");

            expect(mockPost).toHaveBeenCalledWith("/api/v1/share/sess3", {});
        });
    });

    describe("deleteShareLink", () => {
        test("calls DELETE with correct URL", async () => {
            mockDelete.mockResolvedValue(undefined);

            await service.deleteShareLink("share-del");

            expect(mockDelete).toHaveBeenCalledWith("/api/v1/share/share-del");
        });
    });

    describe("addShareUsers", () => {
        test("calls POST with users payload and returns camelCased response", async () => {
            const payload = { shares: [{ userEmail: "x@y.com", accessLevel: "resource_viewer" }] };
            mockPost.mockResolvedValue({ added: [{ userEmail: "x@y.com" }] });

            const result = await service.addShareUsers("share-a", payload);

            expect(mockPost).toHaveBeenCalledWith("/api/v1/share/share-a/users", payload);
            expect(result).toEqual({ added: [{ userEmail: "x@y.com" }] });
        });
    });

    describe("deleteShareUsers", () => {
        test("calls DELETE with body and returns camelCased response on success", async () => {
            const payload = { userEmails: ["a@b.com"] };
            const fakeResponse = {
                ok: true,
                json: vi.fn().mockResolvedValue({ removed: ["a@b.com"] }),
            };
            mockDelete.mockResolvedValue(fakeResponse);

            const result = await service.deleteShareUsers("share-b", payload);

            expect(mockDelete).toHaveBeenCalledWith(
                "/api/v1/share/share-b/users",
                expect.objectContaining({
                    fullResponse: true,
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify(payload),
                })
            );
            expect(result).toEqual({ removed: ["a@b.com"] });
        });

        test("throws when response is not ok", async () => {
            const payload = { userEmails: ["a@b.com"] };
            const fakeResponse = {
                ok: false,
                json: vi.fn().mockResolvedValue({ message: "Not allowed" }),
            };
            mockDelete.mockResolvedValue(fakeResponse);

            await expect(service.deleteShareUsers("share-b", payload)).rejects.toThrow("Not allowed");
        });

        test("throws default message when error json parsing fails", async () => {
            const payload = { userEmails: ["a@b.com"] };
            const fakeResponse = {
                ok: false,
                json: vi.fn().mockRejectedValue(new Error("parse")),
            };
            mockDelete.mockResolvedValue(fakeResponse);

            await expect(service.deleteShareUsers("share-b", payload)).rejects.toThrow("Failed to delete share users");
        });
    });

    describe("listSharedWithMe", () => {
        test("calls GET and returns camelCased list", async () => {
            mockGet.mockResolvedValue({
                data: [{ id: "s1", title: "My Chat", ownerEmail: "owner@test.com" }],
                meta: { pagination: { pageNumber: 1, count: 1, pageSize: 20, nextPage: null, totalPages: 1 } },
            });

            const result = await service.listSharedWithMe();

            expect(mockGet).toHaveBeenCalledWith("/api/v1/share/sharedWithMe");
            expect(result).toEqual([{ id: "s1", title: "My Chat", ownerEmail: "owner@test.com" }]);
        });
    });

    describe("forkSharedChat", () => {
        test("calls POST to fork endpoint and returns camelCased response", async () => {
            mockPost.mockResolvedValue({ sessionId: "forked-sess", id: "s1" });

            const result = await service.forkSharedChat("s1");

            expect(mockPost).toHaveBeenCalledWith("/api/v1/share/s1/fork");
            expect(result).toEqual({ sessionId: "forked-sess", id: "s1" });
        });
    });

    describe("viewSharedSession", () => {
        test("calls GET with credentials and returns camelCased session view", async () => {
            mockGet.mockResolvedValue({
                id: "s1",
                title: "Shared Session",
                tasks: [],
            });

            const result = await service.viewSharedSession("s1");

            expect(mockGet).toHaveBeenCalledWith("/api/v1/share/s1", { credentials: "include" });
            expect(result).toEqual({
                id: "s1",
                title: "Shared Session",
                tasks: [],
            });
        });

        test("preserves snake_case keys inside full_payload (opaque A2A payload)", async () => {
            mockGet.mockResolvedValue({
                id: "s1",
                taskEvents: {
                    t1: {
                        taskId: "t1",
                        events: [
                            {
                                eventType: "a2a_message",
                                fullPayload: {
                                    result: {
                                        status: {
                                            message: {
                                                parts: [
                                                    {
                                                        kind: "data",
                                                        data: {
                                                            type: "tool_invocation_start",
                                                            tool_name: "search",
                                                            tool_args: { query_text: "hello" },
                                                            function_call_id: "fc1",
                                                        },
                                                    },
                                                ],
                                            },
                                        },
                                    },
                                },
                            },
                        ],
                    },
                },
            });

            const result = (await service.viewSharedSession("s1")) as unknown as {
                taskEvents: { t1: { events: { fullPayload: Record<string, unknown> }[] } };
            };

            const payload = result.taskEvents.t1.events[0].fullPayload;
            const data = (payload as { result: { status: { message: { parts: { data: Record<string, unknown> }[] } } } }).result.status.message.parts[0].data;

            expect(data.tool_name).toBe("search");
            expect(data.function_call_id).toBe("fc1");
            expect(data.tool_args).toEqual({ query_text: "hello" });
            expect(data.toolName).toBeUndefined();
            expect(data.functionCallId).toBeUndefined();
        });
    });

    describe("downloadSharedArtifact", () => {
        test("calls GET with fullResponse and returns blob on success", async () => {
            const fakeBlob = new Blob(["data"], { type: "application/pdf" });
            const fakeResponse = {
                ok: true,
                blob: vi.fn().mockResolvedValue(fakeBlob),
            };
            mockGet.mockResolvedValue(fakeResponse);

            const result = await service.downloadSharedArtifact("s1", "report.pdf");

            expect(mockGet).toHaveBeenCalledWith("/api/v1/share/s1/artifacts/report.pdf", {
                fullResponse: true,
                credentials: "include",
            });
            expect(result).toBe(fakeBlob);
        });

        test("encodes the filename in the URL", async () => {
            const fakeBlob = new Blob(["data"]);
            const fakeResponse = {
                ok: true,
                blob: vi.fn().mockResolvedValue(fakeBlob),
            };
            mockGet.mockResolvedValue(fakeResponse);

            await service.downloadSharedArtifact("s1", "my file (1).pdf");

            expect(mockGet).toHaveBeenCalledWith("/api/v1/share/s1/artifacts/my%20file%20(1).pdf", expect.any(Object));
        });

        test("throws when response is not ok", async () => {
            const fakeResponse = {
                ok: false,
                statusText: "Not Found",
            };
            mockGet.mockResolvedValue(fakeResponse);

            await expect(service.downloadSharedArtifact("s1", "missing.pdf")).rejects.toThrow("Failed to download: Not Found");
        });
    });

    describe("updateShareLink", () => {
        test("calls PATCH and returns camelCased result", async () => {
            mockPatch.mockResolvedValue({ id: "s1", isActive: true });

            const result = await service.updateShareLink("s1", { requireAuthentication: true });

            expect(mockPatch).toHaveBeenCalledWith("/api/v1/share/s1", { requireAuthentication: true });
            expect(result).toEqual({ id: "s1", isActive: true });
        });
    });

    describe("listShareLinks", () => {
        test("calls GET without query params when none provided", async () => {
            mockGet.mockResolvedValue({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } });

            await service.listShareLinks();

            expect(mockGet).toHaveBeenCalledWith("/api/v1/share");
        });

        test("appends query params when provided", async () => {
            mockGet.mockResolvedValue({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } });

            await service.listShareLinks({ pageNumber: 2, pageSize: 25, search: "test" });

            expect(mockGet).toHaveBeenCalledWith("/api/v1/share?pageNumber=2&pageSize=25&search=test");
        });
    });

    describe("updateShareSnapshot", () => {
        test("calls POST with user email body and returns camelCased result", async () => {
            mockPost.mockResolvedValue({ snapshotTime: 1700000000 });

            const result = await service.updateShareSnapshot("s1", "user@test.com");

            expect(mockPost).toHaveBeenCalledWith("/api/v1/share/s1/updateSnapshot", {
                userEmail: "user@test.com",
            });
            expect(result).toEqual({ snapshotTime: 1700000000 });
        });

        test("calls POST without body when no email provided", async () => {
            mockPost.mockResolvedValue({ snapshotTime: 1700000000 });

            await service.updateShareSnapshot("s1");

            expect(mockPost).toHaveBeenCalledWith("/api/v1/share/s1/updateSnapshot", undefined);
        });
    });

    describe("markShareViewed", () => {
        test("calls POST to the viewed endpoint and returns the new timestamp", async () => {
            mockPost.mockResolvedValue({ viewedAt: "2024-01-01T00:00:00.000000+00:00" });

            const result = await service.markShareViewed("s1");

            expect(mockPost).toHaveBeenCalledWith("/api/v1/share/s1/viewed");
            expect(result).toEqual({ viewedAt: "2024-01-01T00:00:00.000000+00:00" });
        });
    });
});
