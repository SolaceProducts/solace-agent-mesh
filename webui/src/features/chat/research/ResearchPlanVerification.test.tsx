// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { type ResearchPlanData, ResearchPlanVerification } from "@/features/chat/research/ResearchPlanVerification";
import { __resetRespondedPlansForTest } from "@/features/chat/research/respondedPlansStore";

import { StoryProvider } from "@/testing/providers/StoryProvider";

const basePlan: ResearchPlanData = {
    type: "deep_research_plan",
    plan_id: "plan-1",
    agent_name: "ResearchAgent",
    title: "Demo plan",
    research_question: "Why is the sky blue?",
    steps: ["Step one", "Step two"],
    research_type: "quick",
    max_iterations: 2,
    max_runtime_seconds: 60,
    sources: ["web"],
};

function renderComponent(planData: ResearchPlanData = basePlan, chatContextValues = {}) {
    return render(
        <StoryProvider chatContextValues={chatContextValues}>
            <ResearchPlanVerification planData={planData} />
        </StoryProvider>
    );
}

function makeFetchMock(response: Partial<Response> & { ok?: boolean } = { ok: true }) {
    return vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ status: "ok" }),
        text: () => Promise.resolve(""),
        ...response,
    });
}

describe("ResearchPlanVerification", () => {
    beforeEach(() => {
        vi.unstubAllGlobals();
        __resetRespondedPlansForTest();
    });

    test("renders plan title and steps", () => {
        renderComponent();
        expect(screen.getByText("Demo plan")).toBeInTheDocument();
        expect(screen.getByText("Step one")).toBeInTheDocument();
        expect(screen.getByText("Step two")).toBeInTheDocument();
    });

    test("renders nothing when the plan is already responded", () => {
        const { container } = renderComponent({ ...basePlan, responded: "start" });
        expect(container).toBeEmptyDOMElement();
    });

    test("clicking Start POSTs to /research/planResponse and marks the message responded", async () => {
        const fetchMock = makeFetchMock();
        vi.stubGlobal("fetch", fetchMock);
        const setMessages = vi.fn();

        renderComponent(basePlan, { setMessages });
        fireEvent.click(screen.getByRole("button", { name: /start/i }));

        await waitFor(() => expect(fetchMock).toHaveBeenCalled());
        const [url, init] = fetchMock.mock.calls[0];
        expect(String(url)).toContain("/api/v1/research/planResponse");
        const body = JSON.parse(String(init?.body ?? "{}"));
        expect(body).toMatchObject({ planId: "plan-1", agentName: "ResearchAgent", action: "start", steps: basePlan.steps });
        expect(setMessages).toHaveBeenCalled();
    });

    test("clicking Cancel invokes the chat handleCancel (same as stop button)", async () => {
        const fetchMock = makeFetchMock();
        vi.stubGlobal("fetch", fetchMock);
        const handleCancel = vi.fn();

        renderComponent(basePlan, { handleCancel });
        fireEvent.click(screen.getByRole("button", { name: /cancel/i }));

        expect(handleCancel).toHaveBeenCalled();
        const planResponseCalls = fetchMock.mock.calls.filter(([url]) => String(url).includes("/research/planResponse"));
        expect(planResponseCalls.length).toBe(0);
    });

    test("surfaces a displayError when the mutation fails", async () => {
        vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("boom")));
        const displayError = vi.fn();

        renderComponent(basePlan, { displayError });
        fireEvent.click(screen.getByRole("button", { name: /start/i }));

        await waitFor(() => {
            expect(displayError).toHaveBeenCalledWith(expect.objectContaining({ title: expect.any(String), error: expect.any(String) }));
        });
    });
});
