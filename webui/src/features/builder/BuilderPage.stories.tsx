// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/* eslint-disable @typescript-eslint/no-explicit-any -- chat-context shape is wide; mocks elide fields BuilderPage doesn't read at idle. */
import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { BuilderPage } from "@/features/builder/BuilderPage";

import { getPaginatedMeta, withDefaults } from "@/testing/helpers";

const SAMPLE_MANIFEST_YAML = `
name: OrderFlow
display_name: Order Processing Workflow
description: Validates and routes incoming customer orders
components:
  - filename: order_workflow.yaml
    type: workflow
    name: OrderProcessingWorkflow
    status: validated
    origin: session
  - filename: validator.yaml
    type: agent
    name: Validator
    status: validated
    origin: session
  - filename: notifier.yaml
    type: agent
    name: Notifier
    status: validated
    origin: session
`.trim();

const SAMPLE_MANIFEST_ARTIFACT = {
    filename: "manifest.yaml",
    mimeType: "application/vnd.sam-build-manifest+yaml",
    version: 1,
    accumulatedContent: SAMPLE_MANIFEST_YAML,
    size: SAMPLE_MANIFEST_YAML.length,
    lastModified: new Date().toISOString(),
};

const meta = {
    title: "Builder/BuilderPage",
    component: BuilderPage,
    parameters: {
        routerValues: { initialPath: "/builder" },
        chatContext: {
            sessionId: "builder-session-1",
            allArtifacts: [SAMPLE_MANIFEST_ARTIFACT] as any,
            downloadAndResolveArtifact: (async () => SAMPLE_MANIFEST_YAML) as any,
            agents: [{ name: "Builder", displayName: "Builder", description: "Build agents and workflows" }] as any,
        },
        msw: {
            handlers: withDefaults(
                http.get("*/api/v1/sessions/:id", ({ params }) => HttpResponse.json({ id: params.id, agentId: "Builder", name: "Builder Session" })),
                http.get("*/api/v1/artifacts/:sessionId", () => HttpResponse.json({ data: [SAMPLE_MANIFEST_ARTIFACT], meta: getPaginatedMeta(1, 1) })),
                http.get("*/api/v1/artifacts/:sessionId/:filename/versions", () => HttpResponse.json({ data: [] })),
                http.get("*/api/v1/sessions/:sessionId/contextUsage", () =>
                    HttpResponse.json({
                        sessionId: "mock",
                        currentContextTokens: 0,
                        promptTokens: 0,
                        completionTokens: 0,
                        cachedTokens: 0,
                        maxInputTokens: null,
                        usagePercentage: 0,
                        model: "mock",
                        totalEvents: 0,
                        totalMessages: 0,
                        totalTasks: 0,
                        hasCompaction: false,
                    })
                ),
                http.patch("*/api/v1/sessions/:sessionId", () => HttpResponse.json({})),
                http.get("*/api/internal/platform/builder/sessions/:sessionId/test-agent", () => new HttpResponse(null, { status: 404 }))
            ),
        },
    },
} satisfies Meta<typeof BuilderPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const workflowMatches = await canvas.findAllByText("OrderProcessingWorkflow", undefined, { timeout: 5000 });
        expect(workflowMatches.length).toBeGreaterThan(0);
        await canvas.findAllByText("Validator", undefined, { timeout: 5000 });
        await canvas.findAllByText("Notifier", undefined, { timeout: 5000 });

        await canvas.findByText("Order Processing Workflow");
    },
};
