// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router-dom";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import { mockAgents } from "@/testing/data/agents";
import { anthropicModelConfig, typesafeDecisionModelConfig } from "@/testing/data/models";
import { toModelV2 } from "@/testing/data/modelsV2";
import { paginatedResponse } from "@/testing/helpers/handlers";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { AgentModelEdit } from "./AgentModelEdit";

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderEditor(modelRef?: string) {
    const agent = { ...mockAgents[0], modelProvider: modelRef ? [modelRef] : [] };
    return render(
        <StoryProvider>
            <MemoryRouter>
                <AgentModelEdit agent={agent} onApply={vi.fn()} onDiscard={vi.fn()} />
            </MemoryRouter>
        </StoryProvider>
    );
}

describe("AgentModelEdit", () => {
    test("offers chat models only, never a decision model", async () => {
        server.use(http.get("*/api/v2/platform/models", () => HttpResponse.json(paginatedResponse([anthropicModelConfig, typesafeDecisionModelConfig].map(toModelV2)))));
        renderEditor();

        expect(await screen.findByText(anthropicModelConfig.alias)).toBeInTheDocument();
        expect(screen.queryByText(typesafeDecisionModelConfig.alias)).toBeNull();
    });

    test("treats an agent pointing at a decision model as having no usable model", async () => {
        server.use(http.get("*/api/v2/platform/models", () => HttpResponse.json(paginatedResponse([anthropicModelConfig, typesafeDecisionModelConfig].map(toModelV2)))));
        renderEditor(typesafeDecisionModelConfig.id);

        expect(await screen.findByText(/Previously selected model is no longer available/i)).toBeInTheDocument();
    });
});
