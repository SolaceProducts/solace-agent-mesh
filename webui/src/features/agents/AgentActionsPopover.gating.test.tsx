// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, test } from "vitest";

import { CAPABILITIES, type Capability } from "@/providers/contexts/UserContext";

import { getPaginatedMeta } from "@/testing/helpers";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { Agent, AgentCardInfo } from "@/types";

import { AgentActionsPopover } from "./AgentActionsPopover";

expect.extend(matchers);

const server = setupServer(
    http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/config", () => HttpResponse.json({})),
    http.get("*/api/v1/speech/config", () => HttpResponse.json({}))
);

beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const agent = { name: "ext", displayName: "External Agent", type: "external" } as unknown as AgentCardInfo;
const userAgent = { id: "remote-9", name: "ext" } as unknown as Agent;

const openMenuWith = async (granted: Capability[]) => {
    const held = new Set<string>(granted);
    renderWithProviders(
        <AgentActionsPopover agent={agent} userAgent={userAgent} selectedState="inactive">
            <button data-testid="moreTrigger">Actions</button>
        </AgentActionsPopover>,
        { userContextValues: { userHasCapability: (capability: Capability) => held.has(capability) } }
    );
    await userEvent.click(screen.getByTestId("moreTrigger"));
    return screen.findByRole("dialog");
};

describe("AgentActionsPopover per-verb menu items", () => {
    test("a reader gets the read affordance and no write actions", async () => {
        const menu = await openMenuWith([CAPABILITIES.readAgents]);

        expect(await screen.findByText("Open Agent")).toBeInTheDocument();
        expect(menu).not.toHaveTextContent("Edit");
        expect(menu).not.toHaveTextContent("Delete");
        expect(menu).not.toHaveTextContent("Deploy");
    });

    test("update alone exposes Edit only", async () => {
        const menu = await openMenuWith([CAPABILITIES.updateAgents]);

        expect(await screen.findByText("Edit")).toBeInTheDocument();
        expect(menu).not.toHaveTextContent("Delete");
        expect(menu).not.toHaveTextContent("Deploy");
    });

    test("delete alone exposes Delete only", async () => {
        const menu = await openMenuWith([CAPABILITIES.deleteAgents]);

        expect(await screen.findByText("Delete")).toBeInTheDocument();
        expect(menu).not.toHaveTextContent("Edit");
        expect(menu).not.toHaveTextContent("Deploy");
    });

    test("deploy alone exposes Deploy only", async () => {
        const menu = await openMenuWith([CAPABILITIES.deployAgents]);

        expect(await screen.findByText("Deploy")).toBeInTheDocument();
        expect(menu).not.toHaveTextContent("Edit");
        expect(menu).not.toHaveTextContent("Delete");
    });
});
