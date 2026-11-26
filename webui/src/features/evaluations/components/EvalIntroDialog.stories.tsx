// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, fn, screen, userEvent, waitFor } from "storybook/test";

import { Button } from "@/ui/button";

import { useEvalIntroDialog } from "@/features/evaluations/hooks";

import { experimentToV2 } from "@/testing/data/evaluationsV2";
import { getPaginatedMeta, withDefaults } from "@/testing/helpers";

import { EvalIntroDialog } from "./EvalIntroDialog";

function EvalIntroDialogWithHook({ agentName }: { agentName: string }) {
    const { evalIntroOpen, setEvalIntroOpen, tryOpenEvalIntro, evalIntroDismissed, setEvalIntroDismissed } = useEvalIntroDialog();
    return (
        <div>
            <Button data-testid="deployTrigger" onClick={() => void tryOpenEvalIntro(agentName)}>
                Deploy Agent
            </Button>
            <EvalIntroDialog open={evalIntroOpen} onOpenChange={setEvalIntroOpen} dismissed={evalIntroDismissed} onDismissedChange={setEvalIntroDismissed} />
        </div>
    );
}

const EXPERIMENTS_URL = "*/api/v2/platform/evaluations/experiments";
const TEST_AGENT = "agent_019ddee5_159d_7f10_9d1c_c8ac439386ed";

const emptyMeta = { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } };
const handlersNoExperiments = withDefaults(http.get(EXPERIMENTS_URL, () => HttpResponse.json({ data: [], meta: emptyMeta })));

const handlersWithExperiments = withDefaults(
    http.get(EXPERIMENTS_URL, ({ request }) => {
        const targetAgent = new URL(request.url).searchParams.get("targetAgent");
        if (targetAgent !== TEST_AGENT) return HttpResponse.json({ data: [], meta: emptyMeta });
        const data = [
            experimentToV2({
                id: "exp-001",
                name: "Agent Accuracy Test",
                description: "Testing agent accuracy",
                targetAgent: TEST_AGENT,
                datasetId: "ds-001",
                models: [],
                runsPerExample: 2,
                maxWorkers: 4,
                evaluatorIds: [],
                createdBy: "",
                updatedBy: "",
                createdAt: new Date().toISOString(),
                updatedAt: new Date().toISOString(),
            }),
        ];
        return HttpResponse.json({
            data,
            meta: getPaginatedMeta(data.length),
        });
    })
);

const meta = {
    title: "Evaluations/EvalIntroDialog",
    component: EvalIntroDialog,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component:
                    "Intro dialog shown after deploying a non-A2A agent without existing experiments. Prompts the user to set up evaluations. Respects a global localStorage dismissed flag and checks whether the agent already has experiments before showing.",
            },
        },
    },
    args: {
        open: true,
        onOpenChange: fn(),
        dismissed: false,
        onDismissedChange: fn(),
    },
} satisfies Meta<typeof EvalIntroDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        await expect(dialog).toBeInTheDocument();
        await expect(screen.getByText("Set Up Experiments and Coach Your Agents")).toBeInTheDocument();
        await expect(screen.getByText(/Experiments validate that your agents/i)).toBeInTheDocument();
        await expect(screen.getByRole("button", { name: /Get Started/i })).toBeInTheDocument();
        await expect(screen.getByRole("button", { name: /Close/i })).toBeInTheDocument();
    },
};

export const CloseButton: Story = {
    play: async ({ args }) => {
        await screen.findByRole("dialog");
        await userEvent.click(screen.getByRole("button", { name: /Close/i }));
        await expect(args.onOpenChange).toHaveBeenCalledWith(false);
    },
};

export const HookOpensWhenAgentHasNoExperiments: Story = {
    render: () => <EvalIntroDialogWithHook agentName={TEST_AGENT} />,
    parameters: {
        msw: { handlers: handlersNoExperiments },
    },
    beforeEach: () => {
        localStorage.removeItem("eval-intro-dialog-dismissed");
    },
    play: async ({ canvasElement }) => {
        const trigger = canvasElement.querySelector("[data-testid='deployTrigger']") as HTMLElement;
        await userEvent.click(trigger);

        await waitFor(async () => {
            const dialog = await screen.findByRole("dialog");
            await expect(dialog).toBeInTheDocument();
        });
        await expect(screen.getByText("Set Up Experiments and Coach Your Agents")).toBeInTheDocument();
    },
};

export const HookSuppressedWhenAgentHasExperiments: Story = {
    render: () => <EvalIntroDialogWithHook agentName={TEST_AGENT} />,
    parameters: {
        msw: { handlers: handlersWithExperiments },
    },
    beforeEach: () => {
        localStorage.removeItem("eval-intro-dialog-dismissed");
    },
    play: async ({ canvasElement }) => {
        const trigger = canvasElement.querySelector("[data-testid='deployTrigger']") as HTMLElement;
        await userEvent.click(trigger);

        await waitFor(() => {
            expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
        });
    },
};

export const HookSuppressedWhenDismissed: Story = {
    render: () => <EvalIntroDialogWithHook agentName={TEST_AGENT} />,
    parameters: {
        msw: { handlers: handlersNoExperiments },
    },
    beforeEach: () => {
        localStorage.setItem("eval-intro-dialog-dismissed", "true");
    },
    play: async ({ canvasElement }) => {
        const trigger = canvasElement.querySelector("[data-testid='deployTrigger']") as HTMLElement;
        await userEvent.click(trigger);

        await waitFor(() => {
            expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
        });
    },
    afterEach: () => {
        localStorage.removeItem("eval-intro-dialog-dismissed");
    },
};
