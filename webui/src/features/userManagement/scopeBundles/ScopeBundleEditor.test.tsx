// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { COMPONENT_SCOPES } from "./bundles";
import { DEMO_AGENTS, DEMO_WORKFLOWS } from "./fixtures";
import { ScopeBundleEditor } from "./ScopeBundleEditor";

function Harness({ initial = [], onChange }: { initial?: string[]; onChange?: (scopes: string[]) => void }) {
    const [value, setValue] = useState<string[]>(initial);
    return (
        <ScopeBundleEditor
            value={value}
            onChange={next => {
                setValue(next);
                onChange?.(next);
            }}
            agents={DEMO_AGENTS}
            workflows={DEMO_WORKFLOWS}
        />
    );
}

const agentScope = (id: string) => `agent:${id}:invoke`;
const workflowScope = (id: string) => `workflow:${id}:invoke`;

describe("ScopeBundleEditor — Access tab", () => {
    test("opens on the Access tab by default", async () => {
        renderWithProviders(<Harness />);

        expect(await screen.findByText("Agents (0)")).toBeInTheDocument();
        expect(screen.getByText("Workflows (0)")).toBeInTheDocument();
    });

    test("adds and removes an individual agent", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        const agent = DEMO_AGENTS[0];
        renderWithProviders(<Harness onChange={onChange} />);

        await user.click(screen.getByRole("button", { name: /Add Agent/ }));
        await user.click(await screen.findByText(agent.name));

        expect(screen.getByText("Agents (1)")).toBeInTheDocument();
        expect(onChange).toHaveBeenLastCalledWith(expect.arrayContaining([agentScope(agent.id)]));

        await user.click(screen.getByRole("button", { name: "Remove" }));
        expect(screen.getByText("Agents (0)")).toBeInTheDocument();
    });

    test("All Agents with no individuals grants the wildcard without confirmation", async () => {
        const user = userEvent.setup();
        renderWithProviders(<Harness />);

        await user.click(screen.getByRole("button", { name: /Add Agent/ }));
        await user.click(await screen.findByText("All Agents"));

        expect(screen.getByText("Agents (All)")).toBeInTheDocument();
        expect(screen.queryByTestId("dialogConfirmButton")).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /Add Agent/ })).not.toBeInTheDocument();
    });

    test("All Agents replaces individual agents but keeps workflows", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        const agent = DEMO_AGENTS[0];
        const workflow = DEMO_WORKFLOWS[0];
        renderWithProviders(<Harness onChange={onChange} />);

        await user.click(screen.getByRole("button", { name: /Add Workflow/ }));
        await user.click(await screen.findByText(workflow.name));

        await user.click(screen.getByRole("button", { name: /Add Agent/ }));
        await user.click(await screen.findByText(agent.name));

        await user.click(screen.getByRole("button", { name: /Add Agent/ }));
        await user.click(await screen.findByText("All Agents"));
        await user.click(await screen.findByTestId("dialogConfirmButton"));

        const last = onChange.mock.lastCall?.[0] as string[];
        expect(last).toContain("agent:*:invoke");
        expect(last).not.toContain(agentScope(agent.id));
        expect(last).toContain(workflowScope(workflow.id));
    });

    test("cancelling All Agents keeps the individual agent", async () => {
        const user = userEvent.setup();
        const agent = DEMO_AGENTS[0];
        renderWithProviders(<Harness />);

        await user.click(screen.getByRole("button", { name: /Add Agent/ }));
        await user.click(await screen.findByText(agent.name));
        expect(screen.getByText("Agents (1)")).toBeInTheDocument();

        await user.click(screen.getByRole("button", { name: /Add Agent/ }));
        await user.click(await screen.findByText("All Agents"));
        const dialog = await screen.findByRole("dialog");
        await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

        expect(screen.getByText("Agents (1)")).toBeInTheDocument();
        expect(screen.queryByText("Agents (All)")).not.toBeInTheDocument();
    });

    test("a role carrying the wildcard invoke grants shows removable All rows and no info banner", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        renderWithProviders(<Harness initial={[...COMPONENT_SCOPES]} onChange={onChange} />);

        expect(screen.getByText("Agents (All)")).toBeInTheDocument();
        expect(screen.getByText("Workflows (All)")).toBeInTheDocument();

        const removeButtons = screen.getAllByRole("button", { name: "Remove" });
        expect(removeButtons).toHaveLength(2);
        await user.click(removeButtons[0]);
        expect(onChange.mock.lastCall?.[0]).not.toContain("agent:*:invoke");
    });
});

const FLAG_ON = { feedback_management: true };
const fbScope = (kind: "agent" | "workflow", id: string) => (id === "*" ? `${kind}:*:read_feedback` : `${kind}:${id}:read_feedback`);
const trScope = (id: string) => (id === "*" ? "agent:*:read_trace" : `agent:${id}:read_trace`);

describe("ScopeBundleEditor — Access tab, feedback_management on", () => {
    test("All Agents with no individuals grants the wildcard without confirmation, and hides Add", async () => {
        const user = userEvent.setup();
        renderWithProviders(<Harness />, { featureFlags: FLAG_ON });

        await user.click(screen.getByRole("button", { name: /Add Agent/ }));
        await user.click(await screen.findByText("All Agents"));

        expect(screen.getByText("All Agents")).toBeInTheDocument();
        expect(screen.queryByTestId("dialogConfirmButton")).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /Add Agent/ })).not.toBeInTheDocument();
    });

    test("All Agents replaces a specific agent's grants, including feedback and traces, after confirmation", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        const agent = DEMO_AGENTS[0];
        renderWithProviders(<Harness initial={[agentScope(agent.id), fbScope("agent", agent.id), trScope(agent.id)]} onChange={onChange} />, { featureFlags: FLAG_ON });

        expect(screen.getByText("Agents (1)")).toBeInTheDocument();
        await user.click(screen.getByRole("button", { name: /Add Agent/ }));
        await user.click(await screen.findByText("All Agents"));
        await user.click(await screen.findByTestId("dialogConfirmButton"));

        const last = onChange.mock.lastCall?.[0] as string[];
        expect(last).toEqual(["agent:*:invoke"]);
        expect(screen.getByText("All Agents")).toBeInTheDocument();
        expect(screen.queryByText(agent.name)).not.toBeInTheDocument();
    });

    test("cancelling the All Agents replace keeps the individual agent and its grants", async () => {
        const user = userEvent.setup();
        const agent = DEMO_AGENTS[0];
        renderWithProviders(<Harness initial={[agentScope(agent.id), fbScope("agent", agent.id)]} />, { featureFlags: FLAG_ON });

        await user.click(screen.getByRole("button", { name: /Add Agent/ }));
        await user.click(await screen.findByText("All Agents"));
        const dialog = await screen.findByRole("dialog");
        await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

        expect(screen.getByText("Agents (1)")).toBeInTheDocument();
        expect(screen.queryByText("All Agents")).not.toBeInTheDocument();
        expect(screen.getByRole("checkbox", { name: /Can view feedback$/ })).toBeChecked();
    });

    test("traces and feedback are granted and revoked independently", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        const agent = DEMO_AGENTS[0];
        renderWithProviders(<Harness initial={[agentScope(agent.id)]} onChange={onChange} />, { featureFlags: FLAG_ON });

        expect(screen.getByRole("checkbox", { name: /Can view activities$/ })).toBeEnabled();
        await user.click(screen.getByRole("checkbox", { name: /Can view activities$/ }));
        expect(onChange.mock.lastCall?.[0]).toEqual([agentScope(agent.id), trScope(agent.id)]);

        await user.click(screen.getByRole("checkbox", { name: /Can view feedback$/ }));
        expect(onChange.mock.lastCall?.[0]).toEqual([agentScope(agent.id), trScope(agent.id), fbScope("agent", agent.id)]);

        await user.click(screen.getByRole("checkbox", { name: /Can view feedback$/ }));
        expect(onChange.mock.lastCall?.[0]).toEqual([agentScope(agent.id), trScope(agent.id)]);
        expect(screen.getByRole("checkbox", { name: /Can view activities$/ })).toBeChecked();
    });

    test("ticking feedback on All Agents leaves All Workflows untouched and unrendered", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        renderWithProviders(<Harness initial={["agent:*:invoke"]} onChange={onChange} />, { featureFlags: FLAG_ON });
        await user.click(screen.getByRole("checkbox", { name: /Can view feedback$/ }));
        expect(onChange.mock.lastCall?.[0]).toEqual(["agent:*:invoke", "agent:*:read_feedback"]);
        expect(screen.queryByText("All Workflows")).not.toBeInTheDocument();
    });

    test("removing All Agents leaves All Workflows and its feedback grant intact", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        renderWithProviders(<Harness initial={["agent:*:invoke", "agent:*:read_feedback", "workflow:*:invoke", "workflow:*:read_feedback"]} onChange={onChange} />, { featureFlags: FLAG_ON });

        const removes = screen.getAllByRole("button", { name: "Remove" });
        expect(removes).toHaveLength(2);
        await user.click(removes[0]);

        expect(onChange.mock.lastCall?.[0]).toEqual(["workflow:*:invoke", "workflow:*:read_feedback"]);
        expect(screen.queryByText("All Agents")).not.toBeInTheDocument();
        expect(screen.getByText("All Workflows")).toBeInTheDocument();
        expect(screen.getByRole("checkbox", { name: /Can view feedback$/ })).toBeChecked();
    });

    test("a role with only a feedback grant still shows its card", () => {
        const agent = DEMO_AGENTS[1];
        renderWithProviders(<Harness initial={[fbScope("agent", agent.id)]} />, { featureFlags: FLAG_ON });
        expect(screen.getByText("Agents (1)")).toBeInTheDocument();
        expect(screen.getByText(agent.name)).toBeInTheDocument();
        expect(screen.getByRole("checkbox", { name: /Can view feedback$/ })).toBeChecked();
    });

    test("a feedback-only wildcard renders All Agents only, and removing it yields no scopes", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        renderWithProviders(<Harness initial={["agent:*:read_feedback"]} onChange={onChange} />, { featureFlags: FLAG_ON });

        expect(screen.getByText("All Agents")).toBeInTheDocument();
        expect(screen.queryByText("All Workflows")).not.toBeInTheDocument();

        await user.click(screen.getByRole("button", { name: "Remove" }));

        expect(onChange.mock.lastCall?.[0]).toEqual([]);
        expect(screen.queryByText("All Agents")).not.toBeInTheDocument();
    });

    test("a workflow card has no traces checkbox", () => {
        const wf = DEMO_WORKFLOWS[0];
        renderWithProviders(<Harness initial={[workflowScope(wf.id)]} />, { featureFlags: FLAG_ON });
        expect(screen.getByRole("checkbox", { name: /Can view feedback$/ })).toBeInTheDocument();
        expect(screen.queryByRole("checkbox", { name: /Can view activities$/ })).not.toBeInTheDocument();
    });

    test("disabled disables every control", () => {
        renderWithProviders(<ScopeBundleEditor value={["agent:*:invoke", "agent:*:read_feedback"]} onChange={() => {}} agents={DEMO_AGENTS} workflows={DEMO_WORKFLOWS} disabled />, { featureFlags: FLAG_ON });
        for (const box of screen.getAllByRole("checkbox")) expect(box).toBeDisabled();
        expect(screen.queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /Add Agent/ })).not.toBeInTheDocument();
    });
});
