// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { Activation, ClickwrapTerms } from "@/api/activation";
import * as service from "@/api/activation/service";
import type { ApiError } from "@/api/client";

import { ChatContext, type ChatContextValue, ConfigContext, type ConfigContextValue } from "@/providers/contexts";

import { FreeModelTermsPrompt } from "./FreeModelTermsPrompt";

vi.mock("@/common/dialogs/ModelSetupDialog", () => ({
    ModelSetupDialog: ({ open, setupCopy }: { open: boolean; setupCopy?: { title: string } }) => (open ? <div role="dialog">{setupCopy?.title}</div> : null),
}));

vi.mock("@/api/activation/service", () => ({
    getActivation: vi.fn(),
    getTerms: vi.fn(),
    accept: vi.fn(),
    refreshFreeModel: vi.fn(),
    getFreeModel: vi.fn(),
    signInFreeModel: vi.fn(),
    cancelSignIn: vi.fn(),
}));

const onFreeModel: Activation = {
    state: "active",
    signingIn: false,
    account: { subject: "auth0|ada", email: "ada@example.com", name: "Ada Lovelace" },
    agreements: { eulaClickwrapId: "eula-1" },
    freeModelAvailable: true,
    freeModelChosen: true,
};

const EULA_DOCUMENT = { id: "201", name: "End User License Agreement", version: "1.1", url: "https://legal.example.test/eula" };
const CREDITS_DOCUMENT = { id: "202", name: "LLM Credits Terms", version: "1.0", url: "https://legal.example.test/llm-credits" };
const notAgreed: ClickwrapTerms = { agreed: false, checkboxText: "I agree to {#agreement_list#}.", documents: [EULA_DOCUMENT, CREDITS_DOCUMENT] };
const alreadyAgreed: ClickwrapTerms = { ...notAgreed, agreed: true };

function apiError(status: number, message: string): ApiError {
    return Object.assign(new Error(message), { status });
}

function renderPrompt(gateEnabled = true) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    return render(
        <QueryClientProvider client={client}>
            <ConfigContext value={{ activationGateEnabled: gateEnabled } as ConfigContextValue}>
                <ChatContext value={{ hasModelConfigWrite: true } as ChatContextValue}>
                    <FreeModelTermsPrompt />
                </ChatContext>
            </ConfigContext>
        </QueryClientProvider>
    );
}

async function openTheTerms() {
    await userEvent.click(await screen.findByRole("button", { name: "Review Terms" }));
    expect(await screen.findByRole("heading", { name: "Free Model Terms" })).toBeInTheDocument();
}

async function agreeToTheTerms() {
    const box = await screen.findByRole("checkbox", { name: /LLM Credits Terms/ });
    if (!(box as HTMLInputElement).checked) await userEvent.click(box);
    await userEvent.click(screen.getByRole("button", { name: "Agree" }));
}

describe("FreeModelTermsPrompt", () => {
    beforeEach(() => {
        vi.mocked(service.getActivation).mockResolvedValue(onFreeModel);
        vi.mocked(service.getTerms).mockResolvedValue(notAgreed);
        vi.mocked(service.accept).mockResolvedValue(onFreeModel);
    });
    afterEach(() => vi.resetAllMocks());

    test("shows nothing in a build without the gate", () => {
        renderPrompt(false);
        expect(screen.queryByRole("button", { name: "Review Terms" })).not.toBeInTheDocument();
        expect(service.getActivation).not.toHaveBeenCalled();
    });

    test("shows nothing on an install that chose its own model", async () => {
        vi.mocked(service.getActivation).mockResolvedValue({ ...onFreeModel, freeModelChosen: false });
        renderPrompt();
        await waitFor(() => expect(service.getActivation).toHaveBeenCalled());
        expect(screen.queryByRole("button", { name: "Review Terms" })).not.toBeInTheDocument();
    });

    test("asks for the terms only once they are opened", async () => {
        renderPrompt();
        await screen.findByRole("button", { name: "Review Terms" });
        expect(service.getTerms).not.toHaveBeenCalled();

        await openTheTerms();
        expect(service.getTerms).toHaveBeenCalledTimes(1);
        expect(screen.getAllByRole("checkbox"), "only the free model's terms are asked again").toHaveLength(1);
        expect(screen.getByRole("button", { name: "Agree" })).toBeDisabled();
        await userEvent.click(await screen.findByRole("checkbox", { name: /LLM Credits Terms/ }));
        expect(screen.getByRole("button", { name: "Agree" })).toBeEnabled();
    });

    test("agreeing records the agreement, refreshes the free model's sign-in and asks the person to send again", async () => {
        vi.mocked(service.refreshFreeModel).mockResolvedValue({ status: "available", remainingPercent: 90, models: [] });
        renderPrompt();

        await openTheTerms();
        const terms = await screen.findByRole("link", { name: "LLM Credits Terms" });
        expect(terms).toHaveAttribute("href", CREDITS_DOCUMENT.url);
        fireEvent.click(terms);
        await agreeToTheTerms();

        expect(service.accept).toHaveBeenCalledWith({ freeModel: true, openedDocumentIds: [CREDITS_DOCUMENT.id] });
        await waitFor(() => expect(service.refreshFreeModel).toHaveBeenCalledTimes(1));
        await waitFor(() => expect(screen.queryByRole("heading", { name: "Free Model Terms" })).not.toBeInTheDocument());
        expect(screen.getByText(/Send the message again/)).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Review Terms" })).not.toBeInTheDocument();
    });

    test("agreeing while the free model is paused offers the person's own model, not a resend", async () => {
        vi.mocked(service.refreshFreeModel).mockResolvedValue({ status: "paused", remainingPercent: null, models: [] });
        renderPrompt();

        await openTheTerms();
        await agreeToTheTerms();

        await waitFor(() => expect(screen.queryByRole("heading", { name: "Free Model Terms" })).not.toBeInTheDocument());
        expect(screen.getByText(/the free model is paused/)).toBeInTheDocument();
        expect(screen.queryByText(/Send the message again/)).not.toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: "Replace With My Own Model" }));
        expect(screen.getByRole("dialog")).toBeInTheDocument();
    });

    test("an account that had already agreed refreshes at once, recording nothing", async () => {
        vi.mocked(service.getTerms).mockResolvedValue(alreadyAgreed);
        vi.mocked(service.refreshFreeModel).mockResolvedValue({ status: "available", remainingPercent: null, models: [] });
        renderPrompt();

        await userEvent.click(await screen.findByRole("button", { name: "Review Terms" }));

        await waitFor(() => expect(service.refreshFreeModel).toHaveBeenCalledTimes(1));
        expect(await screen.findByText(/Send the message again/)).toBeInTheDocument();
        expect(service.accept).not.toHaveBeenCalled();
    });

    test("an agreement that could not be recorded says so and can be agreed again", async () => {
        vi.mocked(service.accept).mockRejectedValueOnce(apiError(502, "Solace Agent Mesh couldn't record your agreement with Solace.")).mockResolvedValue(onFreeModel);
        vi.mocked(service.refreshFreeModel).mockResolvedValue({ status: "available", remainingPercent: null, models: [] });
        renderPrompt();

        await openTheTerms();
        await agreeToTheTerms();
        expect(await screen.findByText("Solace Agent Mesh couldn't record your agreement with Solace.")).toBeInTheDocument();
        expect(service.refreshFreeModel).not.toHaveBeenCalled();

        await agreeToTheTerms();
        await waitFor(() => expect(service.refreshFreeModel).toHaveBeenCalledTimes(1));
    });

    test("a refresh that cannot reach Solace ID offers to try again", async () => {
        vi.mocked(service.refreshFreeModel).mockResolvedValueOnce({ status: "unavailable", remainingPercent: null, models: [] }).mockRejectedValueOnce(new Error("down")).mockResolvedValue({ status: "available", remainingPercent: 50, models: [] });
        renderPrompt();

        await openTheTerms();
        await agreeToTheTerms();
        expect(await screen.findByText(/Unable to reach Solace ID/)).toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: "Try Again" }));
        expect(await screen.findByText(/Unable to reach Solace ID/)).toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: "Try Again" }));

        await waitFor(() => expect(screen.queryByRole("heading", { name: "Free Model Terms" })).not.toBeInTheDocument());
        expect(screen.getByText(/Send the message again/)).toBeInTheDocument();
        expect(service.refreshFreeModel).toHaveBeenCalledTimes(3);
    });

    test("a sign-in that has expired says so and offers to sign in again", async () => {
        vi.mocked(service.refreshFreeModel).mockResolvedValue({ status: "reauthorization_required", remainingPercent: null, models: [] });
        renderPrompt();

        await openTheTerms();
        await agreeToTheTerms();

        expect(await screen.findByText(/sign-in for the free model has expired/)).toBeInTheDocument();
        expect(screen.getByRole("heading", { name: "Free Model Terms" })).toBeInTheDocument();
        vi.mocked(service.signInFreeModel).mockResolvedValue({ ...onFreeModel, signingIn: true });
        await userEvent.click(screen.getByRole("button", { name: "Sign In Again" }));
        expect(service.signInFreeModel).toHaveBeenCalled();
    });

    test("terms that cannot be loaded can be loaded again", async () => {
        vi.mocked(service.getTerms).mockRejectedValueOnce(apiError(502, "down")).mockResolvedValue(notAgreed);
        renderPrompt();

        await openTheTerms();
        expect(await screen.findByText("Couldn't reach Solace to load the license agreement. Check your network connection and try again.")).toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: "Try Again" }));

        expect(await screen.findByRole("checkbox", { name: /LLM Credits Terms/ })).toBeEnabled();
        expect(service.getTerms).toHaveBeenCalledTimes(2);
        expect(service.refreshFreeModel).not.toHaveBeenCalled();
    });

    test("opening the terms again asks SpotDraft again", async () => {
        vi.mocked(service.refreshFreeModel).mockResolvedValue({ status: "reauthorization_required", remainingPercent: null, models: [] });
        renderPrompt();

        await openTheTerms();
        await userEvent.keyboard("{Escape}");
        await waitFor(() => expect(screen.queryByRole("heading", { name: "Free Model Terms" })).not.toBeInTheDocument());
        await openTheTerms();

        await waitFor(() => expect(service.getTerms).toHaveBeenCalledTimes(2));
    });
});
