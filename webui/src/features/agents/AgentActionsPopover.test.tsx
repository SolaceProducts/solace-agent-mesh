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
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

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

const externalAgent = { name: "ext", displayName: "External Agent", type: "external" } as unknown as AgentCardInfo;
const userAgent = { id: "remote-9", name: "ext" } as unknown as Agent;

const regularAgent = { name: "hr", displayName: "HR Agent", type: "user" } as unknown as AgentCardInfo;
const regularUserAgent = { id: "agent-1", name: "hr" } as unknown as Agent;

describe("AgentActionsPopover — external agent edit", () => {
    test("Edit navigates to /agents/external/<id>/edit", async () => {
        const navigate = vi.fn();
        renderWithProviders(
            <AgentActionsPopover agent={externalAgent} userAgent={userAgent} selectedState="inactive">
                <button data-testid="popoverTrigger">Actions</button>
            </AgentActionsPopover>,
            { chatContextValues: { navigate }, userContextValues: { userHasCapability: () => true } }
        );

        await userEvent.click(screen.getByTestId("popoverTrigger"));
        await userEvent.click(await screen.findByText("Edit"));

        expect(navigate).toHaveBeenCalledWith(`/agents/external/${userAgent.id}/edit`);
    });
});

describe("AgentActionsPopover — View Access", () => {
    test("shows View Access for an agent manager and opens the access dialog", async () => {
        server.use(http.get("*/api/v2/platform/agents/:id/access", () => HttpResponse.json({ data: [{ userId: "u1", displayName: "Alice", userName: null, email: "alice@example.com", isSystemUser: false }], meta: getPaginatedMeta(1) })));
        renderWithProviders(
            <AgentActionsPopover agent={regularAgent} userAgent={regularUserAgent} selectedState="inactive" showViewAccess>
                <button data-testid="popoverTrigger">Actions</button>
            </AgentActionsPopover>,
            { featureFlags: { rbac: true }, userContextValues: { userHasCapability: () => true } }
        );

        await userEvent.click(screen.getByTestId("popoverTrigger"));

        const labels = (await screen.findAllByRole("menuitem")).map(item => item.textContent);
        expect(labels[labels.indexOf("Edit") + 1]).toBe("View Access");

        await userEvent.click(screen.getByText("View Access"));

        expect(await screen.findByText("User Access")).toBeInTheDocument();
        expect(await screen.findByText("Alice")).toBeInTheDocument();
    });

    test("does not show View Access unless the caller opts in (browse/card surfaces)", async () => {
        renderWithProviders(
            <AgentActionsPopover agent={regularAgent} userAgent={regularUserAgent} selectedState="inactive">
                <button data-testid="popoverTrigger">Actions</button>
            </AgentActionsPopover>,
            { featureFlags: { rbac: true }, userContextValues: { userHasCapability: () => true, hasScope: () => true } }
        );

        await userEvent.click(screen.getByTestId("popoverTrigger"));
        await screen.findByText("Edit");
        expect(screen.queryByText("View Access")).not.toBeInTheDocument();
    });

    test("shows View Access for an rbac reader who cannot manage the agent", async () => {
        renderWithProviders(
            <AgentActionsPopover agent={regularAgent} userAgent={regularUserAgent} selectedState="inactive" showViewAccess>
                <button data-testid="popoverTrigger">Actions</button>
            </AgentActionsPopover>,
            { featureFlags: { rbac: true }, userContextValues: { userHasCapability: () => false, hasScope: () => true } }
        );

        await userEvent.click(screen.getByTestId("popoverTrigger"));
        expect(await screen.findByText("View Access")).toBeInTheDocument();
    });

    test("shows View Access for a read-only agent-builder caller", async () => {
        renderWithProviders(
            <AgentActionsPopover agent={regularAgent} userAgent={regularUserAgent} selectedState="active" showViewAccess>
                <button data-testid="popoverTrigger">Actions</button>
            </AgentActionsPopover>,
            { featureFlags: { rbac: true }, userContextValues: { userHasCapability: (cap: string) => cap === "readAgents", hasScope: () => false } }
        );

        await userEvent.click(screen.getByTestId("popoverTrigger"));
        expect(await screen.findByText("View Access")).toBeInTheDocument();
    });

    test("hides View Access when the user can neither manage the agent nor read rbac", () => {
        renderWithProviders(
            <AgentActionsPopover agent={regularAgent} userAgent={regularUserAgent} selectedState="inactive" showViewAccess>
                <button data-testid="popoverTrigger">Actions</button>
            </AgentActionsPopover>,
            { featureFlags: { rbac: true }, userContextValues: { userHasCapability: () => false, hasScope: () => false } }
        );

        expect(screen.queryByTestId("popoverTrigger")).not.toBeInTheDocument();
        expect(screen.queryByText("View Access")).not.toBeInTheDocument();
    });

    test("hides View Access when the agent has no resolved id", async () => {
        renderWithProviders(
            <AgentActionsPopover agent={regularAgent} userAgent={{ name: "hr" } as unknown as Agent} selectedState="inactive" showViewAccess>
                <button data-testid="popoverTrigger">Actions</button>
            </AgentActionsPopover>,
            { featureFlags: { rbac: true }, userContextValues: { userHasCapability: () => true, hasScope: () => true } }
        );

        await userEvent.click(screen.getByTestId("popoverTrigger"));
        await screen.findByText("Edit");
        expect(screen.queryByText("View Access")).not.toBeInTheDocument();
    });

    test("hides View Access when the rbac flag is off", async () => {
        renderWithProviders(
            <AgentActionsPopover agent={regularAgent} userAgent={regularUserAgent} selectedState="inactive" showViewAccess>
                <button data-testid="popoverTrigger">Actions</button>
            </AgentActionsPopover>,
            { featureFlags: { rbac: false }, userContextValues: { userHasCapability: () => true, hasScope: () => true } }
        );

        await userEvent.click(screen.getByTestId("popoverTrigger"));
        await screen.findByText("Edit");
        expect(screen.queryByText("View Access")).not.toBeInTheDocument();
    });
});
