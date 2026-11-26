// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { defaultAgentCard } from "@/testing/data";
import type { AgentCardInfo, AgentState } from "@/types";

import { DeploymentStatus } from "./common";

const deployedSnapshot: AgentCardInfo = {
    ...defaultAgentCard,
    type: "user",
    status: "active",
};

const createStatusAgent = (overrides: Partial<AgentCardInfo> = {}): AgentCardInfo => ({
    ...defaultAgentCard,
    type: "user",
    ...overrides,
});

type Variant = {
    label: string;
    agent: AgentCardInfo;
    selectedState: AgentState;
    showSyncStatus?: boolean;
    expectedStatus: string;
    expectInfo: boolean;
    expectHealthIndicator?: boolean;
};

const VARIANTS: Variant[] = [
    {
        label: "Not Deployed",
        agent: createStatusAgent({ status: "not_deployed" }),
        selectedState: "inactive",
        expectedStatus: "Not Deployed",
        expectInfo: false,
    },
    {
        label: "Deployed · Active · In Sync",
        agent: createStatusAgent({ status: "deployed", syncStatus: "in_sync", deployed: deployedSnapshot }),
        selectedState: "active",
        expectedStatus: "Running",
        expectInfo: false,
    },
    {
        label: "Disconnected · In Sync",
        agent: createStatusAgent({ status: "disconnected", syncStatus: "in_sync", deployed: deployedSnapshot }),
        selectedState: "inactive",
        expectedStatus: "Disconnected",
        expectInfo: false,
    },
    {
        label: "Deployed · Active · Out of Sync",
        agent: createStatusAgent({ status: "deployed", syncStatus: "out_of_sync", deployed: deployedSnapshot }),
        selectedState: "active",
        expectedStatus: "Running",
        expectInfo: true,
    },
    {
        label: "Disconnected · Out of Sync",
        agent: createStatusAgent({ status: "disconnected", syncStatus: "out_of_sync", deployed: deployedSnapshot }),
        selectedState: "inactive",
        expectedStatus: "Disconnected",
        expectInfo: true,
    },
    {
        label: "Deploy Failed",
        agent: createStatusAgent({ status: "deploy_failed" }),
        selectedState: "inactive",
        expectedStatus: "Deployment Failed",
        expectInfo: false,
    },
    {
        label: "Degraded · Active Problem",
        agent: createStatusAgent({
            status: "degraded",
            runtimeProblems: [{ id: "queue:a2a/myAgent", severity: "degraded", detail: "Broker queue provisioning is retrying.", since: "2026-08-30T12:00:00Z" }],
        }),
        selectedState: "active",
        expectedStatus: "Degraded",
        expectInfo: false,
        expectHealthIndicator: true,
    },
    {
        label: "Deploy Failed · Out of Sync · Active",
        agent: createStatusAgent({ status: "deploy_failed", syncStatus: "out_of_sync", deployed: deployedSnapshot }),
        selectedState: "active",
        expectedStatus: "Deployment Failed",
        expectInfo: true,
    },
    {
        label: "Deploy Failed · Out of Sync · Inactive",
        agent: createStatusAgent({ status: "deploy_failed", syncStatus: "out_of_sync", deployed: deployedSnapshot }),
        selectedState: "inactive",
        expectedStatus: "Deployment Failed",
        expectInfo: true,
    },
    {
        label: "Deployed · Out of Sync · Sync Status Hidden",
        agent: createStatusAgent({ status: "deployed", syncStatus: "out_of_sync", deployed: deployedSnapshot }),
        selectedState: "active",
        showSyncStatus: false,
        expectedStatus: "Running",
        expectInfo: false,
    },
];

const meta = {
    title: "Agents/DeploymentStatus",
    component: DeploymentStatus,
} satisfies Meta<typeof DeploymentStatus>;

export default meta;
type Story = StoryObj<typeof meta>;

export const AllVariants: Story = {
    args: {
        agent: VARIANTS[0].agent,
        selectedState: VARIANTS[0].selectedState,
    },
    render: () => (
        <div className="grid max-w-150 grid-cols-[1fr_auto] items-center gap-x-8 gap-y-4 p-5">
            {VARIANTS.map(variant => (
                <React.Fragment key={variant.label}>
                    <span className="text-sm font-medium">{variant.label}</span>
                    <div data-testid={`variant-${variant.label}`}>
                        <DeploymentStatus agent={variant.agent} selectedState={variant.selectedState} showSyncStatus={variant.showSyncStatus ?? true} />
                    </div>
                </React.Fragment>
            ))}
        </div>
    ),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        for (const variant of VARIANTS) {
            const row = within(canvas.getByTestId(`variant-${variant.label}`));
            await row.findByText(variant.expectedStatus);
            if (variant.expectInfo) {
                await row.findByTestId("deploymentStatusInfo");
            } else {
                expect(row.queryByTestId("deploymentStatusInfo")).toBeNull();
            }
            if (variant.expectHealthIndicator) {
                await row.findByTestId("agentHealth-indicator");
            } else {
                expect(row.queryByTestId("agentHealth-indicator")).toBeNull();
            }
        }
    },
};
