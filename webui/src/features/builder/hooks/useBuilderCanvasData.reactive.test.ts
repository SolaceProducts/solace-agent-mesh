// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

const PLACEHOLDER = "<<__SAM_REQUIRED__>>";

const connectorJson = (password: string) => JSON.stringify({ name: "JiraConnector", type: "api", values: { host: "example.atlassian.net", password } });

type ManifestComponent = { filename: string; type: string; name: string; status?: string; origin?: string };

describe("useBuilderCanvasData — reactive connector config refresh", () => {
    let useBuilderCanvasData: typeof import("./useBuilderCanvasData").useBuilderCanvasData;
    let builderKeys: typeof import("@/api/builder").builderKeys;
    let contentByFile: Record<string, string | null>;
    let manifestComponents: ManifestComponent[];
    let client: QueryClient;

    beforeEach(async () => {
        vi.resetModules();
        contentByFile = {};
        manifestComponents = [];
        client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });

        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({ data: [], isLoading: false, refetch: () => Promise.resolve() }),
        }));

        vi.doMock("@/api/remote-agents", () => ({
            useRemoteAgents: () => ({ data: [] }),
        }));

        vi.doMock("@/providers/contexts", () => ({
            useChatContext: () => ({
                downloadAndResolveArtifact: vi.fn().mockResolvedValue(null),
                sessionId: "session-1",
                isResponding: false,
                messages: [],
            }),
        }));

        vi.doMock("./useBuilderManifest", () => ({
            useBuilderManifest: () => ({ manifest: { name: "jira-system", components: manifestComponents } }),
        }));

        vi.doMock("@/api/builder", async importOriginal => {
            const actual = await importOriginal<typeof import("@/api/builder")>();
            return { ...actual, getManifestContent: vi.fn((_sid: string, filename: string) => Promise.resolve(contentByFile[filename] ?? null)) };
        });

        useBuilderCanvasData = (await import("./useBuilderCanvasData")).useBuilderCanvasData;
        builderKeys = (await import("@/api/builder")).builderKeys;
    });

    function wrapper() {
        return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
    }

    const passwordOf = (r: { current: ReturnType<typeof useBuilderCanvasData> }, name: string) => r.current.componentData.get(name)?.connectorMetadata?.values?.password;

    const fireRefetchArtifacts = () => client.invalidateQueries({ queryKey: [...builderKeys.manifests(), "content", "session-1"] });

    test("clears the missing-credential placeholder when the config query refetches", async () => {
        manifestComponents = [{ filename: "jira.json", type: "connector", name: "JiraConnector", status: "validated", origin: "session" }];
        contentByFile = { "jira.json": connectorJson(PLACEHOLDER) };

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper: wrapper() });
        await waitFor(() => expect(passwordOf(result, "JiraConnector")).toBe(PLACEHOLDER));

        contentByFile = { "jira.json": connectorJson("s3cret") };
        await fireRefetchArtifacts();

        await waitFor(() => expect(passwordOf(result, "JiraConnector")).toBe("s3cret"));
    });

    test("routes refetched content to the right card with multiple connectors", async () => {
        manifestComponents = [
            { filename: "jira.json", type: "connector", name: "JiraConnector", status: "validated", origin: "session" },
            { filename: "slack.json", type: "connector", name: "SlackConnector", status: "validated", origin: "session" },
        ];
        contentByFile = { "jira.json": connectorJson(PLACEHOLDER), "slack.json": connectorJson(PLACEHOLDER) };

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper: wrapper() });
        await waitFor(() => {
            expect(passwordOf(result, "JiraConnector")).toBe(PLACEHOLDER);
            expect(passwordOf(result, "SlackConnector")).toBe(PLACEHOLDER);
        });

        contentByFile = { "jira.json": connectorJson("jira-secret"), "slack.json": connectorJson("slack-secret") };
        await fireRefetchArtifacts();

        await waitFor(() => {
            expect(passwordOf(result, "JiraConnector")).toBe("jira-secret");
            expect(passwordOf(result, "SlackConnector")).toBe("slack-secret");
        });
    });

    test("retains the last resolved content when a refetch returns null", async () => {
        manifestComponents = [{ filename: "jira.json", type: "connector", name: "JiraConnector", status: "validated", origin: "session" }];
        contentByFile = { "jira.json": connectorJson("s3cret") };

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper: wrapper() });
        await waitFor(() => expect(passwordOf(result, "JiraConnector")).toBe("s3cret"));

        contentByFile = { "jira.json": null };
        await fireRefetchArtifacts();
        await new Promise(resolve => setTimeout(resolve, 30));

        expect(passwordOf(result, "JiraConnector")).toBe("s3cret");
    });
});
