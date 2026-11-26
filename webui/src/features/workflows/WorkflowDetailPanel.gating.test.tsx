// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { useLocation } from "react-router-dom";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import type { Workflow } from "@/types/workflow";

import { completeOrderWorkflow } from "@/testing/data";
import { workflowToV2 } from "@/testing/data/platformWireV2";
import { v2Page } from "@/testing/helpers/platformResponses";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { WorkflowDetailPanel } from "./WorkflowDetailPanel";

const platformWorkflowsFetch = vi.fn(() => HttpResponse.json({ message: "forbidden" }, { status: 403 }));
const server = setupServer(http.get("*/api/v2/platform/workflows", platformWorkflowsFetch));

beforeAll(() => server.listen());
afterEach(() => {
    server.resetHandlers();
    platformWorkflowsFetch.mockClear();
});
afterAll(() => server.close());

describe("WorkflowDetailPanel — readWorkflows gating", () => {
    test("without readWorkflows, never calls GET /platform/workflows and renders with no Metadata block", async () => {
        renderWithProviders(<WorkflowDetailPanel workflow={completeOrderWorkflow} onClose={vi.fn()} showOpenButton={false} />, {
            userContextValues: { userHasCapability: () => false },
        });

        expect(await screen.findByText("Workflow Details")).toBeInTheDocument();
        expect(screen.queryByText("Created By:")).not.toBeInTheDocument();
        await new Promise(resolve => setTimeout(resolve, 50));
        expect(platformWorkflowsFetch).not.toHaveBeenCalled();
    });

    test("with readWorkflows, resolves the platform workflow and renders Metadata", async () => {
        servePlatformRecord();

        renderWithProviders(<WorkflowDetailPanel workflow={completeOrderWorkflow} onClose={vi.fn()} showOpenButton={false} />, {
            userContextValues: { userHasCapability: () => true },
        });

        await waitFor(() => expect(screen.getByText("Created By:")).toBeInTheDocument());
        expect(screen.getByText("workflow-author@example.com")).toBeInTheDocument();
    });
});

function servePlatformRecord() {
    const record: Workflow = {
        id: "wf-1",
        name: completeOrderWorkflow.name,
        displayName: completeOrderWorkflow.displayName ?? completeOrderWorkflow.name,
        description: "",
        appConfig: {},
        deploymentStatus: "deployed",
        createdBy: "workflow-author@example.com",
        updatedBy: "workflow-editor@example.com",
        createdTime: 1710843000000,
        updatedTime: 1710843000000,
    };
    server.use(http.get("*/api/v2/platform/workflows", () => HttpResponse.json(v2Page([workflowToV2(record)]))));
}

function LocationProbe() {
    const location = useLocation();
    return <div data-testid="location">{location.pathname + location.search}</div>;
}

describe("WorkflowDetailPanel — Manage Workflow", () => {
    const manageButton = () => screen.queryByRole("button", { name: "Manage Workflow" });

    test("for a user who can update workflows, it opens this workflow on the Workflows page", async () => {
        servePlatformRecord();
        renderWithProviders(
            <>
                <WorkflowDetailPanel workflow={completeOrderWorkflow} onClose={vi.fn()} showOpenButton={false} />
                <LocationProbe />
            </>,
            { initialPath: "/agent-mesh?tab=workflows", userContextValues: { userHasCapability: () => true } }
        );

        await userEvent.click(await screen.findByRole("button", { name: "Manage Workflow" }));

        expect(screen.getByTestId("location")).toHaveTextContent("/workflows?id=wf-1");
    });

    test("a user who can read but not update workflows is not offered it", async () => {
        servePlatformRecord();
        renderWithProviders(<WorkflowDetailPanel workflow={completeOrderWorkflow} onClose={vi.fn()} showOpenButton={false} />, {
            userContextValues: { userHasCapability: capability => capability === CAPABILITIES.readWorkflows },
        });

        await waitFor(() => expect(screen.getByText("Created By:")).toBeInTheDocument());
        expect(manageButton()).not.toBeInTheDocument();
    });

    test("without readWorkflows, the button is not offered", async () => {
        renderWithProviders(<WorkflowDetailPanel workflow={completeOrderWorkflow} onClose={vi.fn()} showOpenButton={false} />, {
            userContextValues: { userHasCapability: () => false },
        });

        expect(await screen.findByText("Workflow Details")).toBeInTheDocument();
        expect(manageButton()).not.toBeInTheDocument();
    });

    test("a workflow with no platform record is not offered it, since the Workflows page has no row for it", async () => {
        renderWithProviders(<WorkflowDetailPanel workflow={completeOrderWorkflow} onClose={vi.fn()} showOpenButton={false} />, {
            userContextValues: { userHasCapability: () => true },
        });

        expect(await screen.findByText("Workflow Details")).toBeInTheDocument();
        await new Promise(resolve => setTimeout(resolve, 50));
        expect(manageButton()).not.toBeInTheDocument();
    });

    test("the Workflows list suppresses it even for a capable user", async () => {
        servePlatformRecord();
        renderWithProviders(<WorkflowDetailPanel workflow={completeOrderWorkflow} onClose={vi.fn()} showOpenButton={false} showManageButton={false} />, {
            userContextValues: { userHasCapability: () => true },
        });

        await waitFor(() => expect(screen.getByText("Created By:")).toBeInTheDocument());
        expect(manageButton()).not.toBeInTheDocument();
    });
});
