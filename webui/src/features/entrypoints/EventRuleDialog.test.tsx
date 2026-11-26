// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import type { EventRule } from "@/types/models";

import { EventRuleDialog } from "./EventRuleDialog";
import type { AgentOption } from "./hooks";

const renderDialog = (agents: AgentOption[] = [], initialRule: EventRule | null = null) => render(<EventRuleDialog open onClose={vi.fn()} onSave={vi.fn()} initialRule={initialRule} agents={agents} existingNames={[]} />);

const openAgentSelect = async (user: ReturnType<typeof userEvent.setup>) => {
    const trigger = screen.getAllByRole("combobox")[0];
    await user.click(trigger);
    return screen.findByRole("listbox");
};

describe("EventRuleDialog schemas section labels", () => {
    test("renders the 'Schemas' section heading", () => {
        renderDialog();
        expect(screen.getByText("Schemas")).toBeInTheDocument();
    });

    test("renders the 'Apply input and/or output schemas' checkbox label", () => {
        renderDialog();
        expect(screen.getByText("Apply input and/or output schemas")).toBeInTheDocument();
    });

    test("no longer renders the old 'Structured Invocation' heading", () => {
        renderDialog();
        expect(screen.queryByText("Structured Invocation")).not.toBeInTheDocument();
    });

    test("no longer renders the old 'Enable structured invocation' checkbox label", () => {
        renderDialog();
        expect(screen.queryByText("Enable structured invocation")).not.toBeInTheDocument();
    });
});

describe("EventRuleDialog agent dropdown filters non-deployed agents", () => {
    const deployed: AgentOption = { name: "deployed-agent", displayName: "Deployed Agent", deploymentStatus: "deployed" };
    const notDeployed: AgentOption = { name: "pending-agent", displayName: "Pending Agent", deploymentStatus: "not_deployed" };
    const failed: AgentOption = { name: "failed-agent", displayName: "Failed Agent", deploymentStatus: "deploy_failed" };
    const discovered: AgentOption = { name: "discovered-agent", displayName: "Discovered Agent" };

    test("offers only deployed agents, hiding not_deployed and deploy_failed", async () => {
        const user = userEvent.setup();
        renderDialog([deployed, notDeployed, failed]);

        const listbox = await openAgentSelect(user);

        expect(within(listbox).getByText("Deployed Agent")).toBeInTheDocument();
        expect(within(listbox).queryByText("Pending Agent")).not.toBeInTheDocument();
        expect(within(listbox).queryByText("Failed Agent")).not.toBeInTheDocument();
    });

    test("keeps discovered-only agents (no deploymentStatus) selectable", async () => {
        const user = userEvent.setup();
        renderDialog([discovered, notDeployed]);

        const listbox = await openAgentSelect(user);

        expect(within(listbox).getByText("Discovered Agent")).toBeInTheDocument();
        expect(within(listbox).queryByText("Pending Agent")).not.toBeInTheDocument();
    });

    test("defaults a new rule to the first deployed agent, skipping a leading non-deployed one", () => {
        renderDialog([notDeployed, deployed]);

        const trigger = screen.getAllByRole("combobox")[0];
        expect(trigger).toHaveTextContent("Deployed Agent");
        expect(trigger).not.toHaveTextContent("Pending Agent");
    });

    test("leaves the target empty when no agents are deployable", () => {
        renderDialog([notDeployed, failed]);

        const trigger = screen.getAllByRole("combobox")[0];
        expect(trigger).not.toHaveTextContent("Pending Agent");
        expect(trigger).not.toHaveTextContent("Failed Agent");
    });

    test("pins an existing rule's now-undeployed target so its name still shows", async () => {
        const user = userEvent.setup();
        const rule = {
            name: "existing",
            targetAgent: notDeployed.name,
            subscriptions: [{ topic: "a/b" }],
            messageFormat: "json",
        } as unknown as EventRule;

        renderDialog([deployed, notDeployed], rule);

        const trigger = screen.getAllByRole("combobox")[0];
        expect(trigger).toHaveTextContent("Pending Agent");

        const listbox = await openAgentSelect(user);
        expect(within(listbox).getByText("Pending Agent")).toBeInTheDocument();
        expect(within(listbox).getByText("Deployed Agent")).toBeInTheDocument();
    });
});
