// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { SAM_AGENT_CONFIG_MIME_TYPE, SAM_CONNECTOR_CONFIG_MIME_TYPE } from "../builderUtils";

describe("useBuilderConfig", () => {
    let useBuilderConfig: typeof import("./useBuilderConfig").useBuilderConfig;

    type Artifact = {
        filename: string;
        mimeType: string;
        version?: number;
        accumulatedContent?: string;
    };
    let mockArtifacts: Artifact[];

    beforeEach(async () => {
        vi.resetModules();
        mockArtifacts = [];

        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => ({
                downloadAndResolveArtifact: () => Promise.resolve(null),
                sessionId: "s1",
            }),
        }));

        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({ data: mockArtifacts, isLoading: false, refetch: () => Promise.resolve() }),
        }));

        const mod = await import("./useBuilderConfig");
        useBuilderConfig = mod.useBuilderConfig;
    });

    test("returns a null config when no builder artifacts are present", () => {
        mockArtifacts = [];
        const { result } = renderHook(() => useBuilderConfig());

        expect(result.current.configYaml).toBeNull();
        expect(result.current.configType).toBe("unknown");
        expect(result.current.artifactInfo).toBeNull();
    });

    test("ignores non-builder MIME types", () => {
        mockArtifacts = [{ filename: "note.txt", mimeType: "text/plain", accumulatedContent: "hi" }];
        const { result } = renderHook(() => useBuilderConfig());

        expect(result.current.configYaml).toBeNull();
        expect(result.current.artifactInfo).toBeNull();
    });

    test("detects an agent config from accumulatedContent", () => {
        const agentYaml = `apps:
  - name: MyAgent
    app_config:
      agent_name: MyAgent
      instruction: do stuff
`;
        mockArtifacts = [
            {
                filename: "agent.yaml",
                mimeType: SAM_AGENT_CONFIG_MIME_TYPE,
                version: 0,
                accumulatedContent: agentYaml,
            },
        ];
        const { result } = renderHook(() => useBuilderConfig());

        expect(result.current.configYaml).toBe(agentYaml);
        expect(result.current.configType).toBe("agent");
        expect(result.current.artifactInfo?.filename).toBe("agent.yaml");
    });

    test("detects a connector config from accumulatedContent", () => {
        const connectorJson = JSON.stringify({
            name: "JiraConn",
            type: "api",
            subtype: "openapi",
            values: { base_url: "https://x" },
        });
        mockArtifacts = [
            {
                filename: "connector.json",
                mimeType: SAM_CONNECTOR_CONFIG_MIME_TYPE,
                version: 0,
                accumulatedContent: connectorJson,
            },
        ];
        const { result } = renderHook(() => useBuilderConfig());

        expect(result.current.configType).toBe("connector");
    });

    test("picks the last artifact when there are multiple builder configs", () => {
        mockArtifacts = [
            {
                filename: "old.yaml",
                mimeType: SAM_AGENT_CONFIG_MIME_TYPE,
                version: 0,
                accumulatedContent: "apps:\n  - name: Old\n    app_config:\n      agent_name: Old\n      instruction: x\n",
            },
            {
                filename: "new.yaml",
                mimeType: SAM_AGENT_CONFIG_MIME_TYPE,
                version: 1,
                accumulatedContent: "apps:\n  - name: New\n    app_config:\n      agent_name: New\n      instruction: y\n",
            },
        ];
        const { result } = renderHook(() => useBuilderConfig());

        expect(result.current.artifactInfo?.filename).toBe("new.yaml");
        expect(result.current.configYaml).toContain("New");
    });
});
