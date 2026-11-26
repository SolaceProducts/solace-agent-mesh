// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { useEffect } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { Activation, ActivationAgreements, ClickwrapTerms } from "@/api/activation";
import * as service from "@/api/activation/service";
import type { ApiError } from "@/api/client";

import { productAnalyticsClient } from "@/common/product-analytics/client";

import { ConfigContext, type ConfigContextValue } from "@/providers/contexts";

import { ActivationGate } from "./ActivationGate";
import { useModelSetupRequest } from "./ModelSetupRequest";

const EULA_DOCUMENT = { id: "201", name: "End User License Agreement", version: "1.1", url: "https://legal.example.test/eula" };
const CREDITS_DOCUMENT = { id: "202", name: "LLM Credits Terms", version: "1.0", url: "https://legal.example.test/llm-credits" };
const CHECKBOX_TEXT = "I agree to {#agreement_list#}.";
const notAgreed: ClickwrapTerms = { agreed: false, checkboxText: CHECKBOX_TEXT, documents: [EULA_DOCUMENT, CREDITS_DOCUMENT] };
const agreedTerms: ClickwrapTerms = { ...notAgreed, agreed: true };

const agreementBox = () => screen.findByRole("checkbox", { name: /End User License Agreement/ });
const ownModelButton = () => screen.getByRole("button", { name: "Add My Own Model" });
const getStartedButton = () => screen.getByRole("button", { name: "Get Started" });

async function toTheWelcome() {
    expect(await screen.findByRole("heading", { name: "Welcome to Solace Agent Mesh" })).toBeInTheDocument();
    await waitFor(async () => expect(await agreementBox()).toBeEnabled());
}

async function tick() {
    await userEvent.click(await agreementBox());
}

vi.mock("@/api/activation/service", () => ({
    getActivation: vi.fn(),
    getTerms: vi.fn(),
    signIn: vi.fn(),
    cancelSignIn: vi.fn(),
    accept: vi.fn(),
    refreshFreeModel: vi.fn(),
    getModelsConfigured: vi.fn(),
    setUpFreeModels: vi.fn(),
}));

function withModelsConfigured(configured: boolean) {
    vi.mocked(service.getModelsConfigured).mockResolvedValue(configured);
}

const agreements: ActivationAgreements = { eulaClickwrapId: "eula-1" };
const account = { subject: "auth0|ada", email: "ada@example.com", name: "Ada Lovelace" };
const freeModel = { grantId: "1c58a105-c275-4401-b9af-9b1af81a80af", provider: "openai", apiBase: "https://llm.example.test/v1", generalModelName: "free", planningModelName: "free-planning" };
const signedOut: Activation = { state: "signed_out", signingIn: false, agreements, freeModelAvailable: true, freeModel, freeModelChosen: false };
const signingIn: Activation = { ...signedOut, signingIn: true };
const eulaPending: Activation = { ...signedOut, state: "eula_pending", account };
const active: Activation = { ...signedOut, state: "active" };
const activeOnFreeModel: Activation = { ...active, freeModelChosen: true };

function TheApp({ layoutMounts = true }: Readonly<{ layoutMounts?: boolean }>) {
    const { requested, shown, appShown } = useModelSetupRequest();
    useEffect(() => {
        if (layoutMounts) appShown();
    }, [layoutMounts, appShown]);
    if (!requested) return <p>The app</p>;
    return (
        <button type="button" onClick={shown}>
            Model setup is showing
        </button>
    );
}

function renderGate({ gateEnabled = true, layoutMounts = true } = {}) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const config = { activationGateEnabled: gateEnabled } as ConfigContextValue;
    return render(
        <QueryClientProvider client={client}>
            <ConfigContext value={config}>
                <ActivationGate>
                    <TheApp layoutMounts={layoutMounts} />
                </ActivationGate>
            </ConfigContext>
        </QueryClientProvider>
    );
}

function apiError(status: number, message: string): ApiError {
    return Object.assign(new Error(message), { status });
}

describe("ActivationGate", () => {
    beforeEach(() => {
        withModelsConfigured(false);
        vi.mocked(service.getTerms).mockResolvedValue(notAgreed);
    });
    afterEach(() => vi.resetAllMocks());

    test("renders the app at once in a build without the gate, without asking for the activation", () => {
        renderGate({ gateEnabled: false });
        expect(screen.getByText("The app")).toBeInTheDocument();
        expect(service.getActivation).not.toHaveBeenCalled();
    });

    test("renders the app for an activated install", async () => {
        vi.mocked(service.getActivation).mockResolvedValue(active);
        renderGate();
        expect(await screen.findByText("The app")).toBeInTheDocument();
    });

    test("an install that chose the free model creates its missing models at the start, once", async () => {
        vi.mocked(service.getActivation).mockResolvedValue(activeOnFreeModel);
        vi.mocked(service.setUpFreeModels).mockResolvedValue();
        renderGate();

        expect(await screen.findByText("The app")).toBeInTheDocument();
        await waitFor(() => expect(service.setUpFreeModels).toHaveBeenCalledWith(freeModel, false));
        expect(service.setUpFreeModels).toHaveBeenCalledTimes(1);
    });

    test("an install that chose its own model creates no free models", async () => {
        vi.mocked(service.getActivation).mockResolvedValue(active);
        renderGate();

        expect(await screen.findByText("The app")).toBeInTheDocument();
        expect(service.setUpFreeModels).not.toHaveBeenCalled();
    });

    test("signing in leads to the onboarding, not the app", async () => {
        vi.mocked(service.getActivation).mockResolvedValueOnce(signedOut).mockResolvedValue(eulaPending);
        vi.mocked(service.signIn).mockResolvedValue(signingIn);
        renderGate();

        expect(await screen.findByRole("heading", { name: /Sign in with your Solace ID/ })).toBeInTheDocument();
        expect(screen.getByText("Free model access comes with your Solace ID.")).toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: "Sign In" }));

        expect(service.signIn).toHaveBeenCalledTimes(1);
        expect(await screen.findByRole("status")).toHaveTextContent("Finish signing in with Solace ID in your browser.");
        expect(screen.getByRole("heading", { name: /Sign in with your Solace ID/ })).toHaveClass("sr-only");
        expect(screen.getByText("Free model access comes with your Solace ID.")).not.toBeVisible();
        expect(await screen.findByRole("heading", { name: "Welcome to Solace Agent Mesh" }, { timeout: 5_000 })).toBeInTheDocument();
        await toTheWelcome();
        expect(screen.queryByText("The app")).not.toBeInTheDocument();
    });

    describe("the welcome", () => {
        beforeEach(() => vi.mocked(service.getActivation).mockResolvedValue(eulaPending));

        test("keeps the box and buttons disabled while the terms load", async () => {
            vi.mocked(service.getTerms).mockImplementation(() => new Promise(() => {}));
            renderGate();

            expect(await screen.findByRole("heading", { name: "Welcome to Solace Agent Mesh" })).toBeInTheDocument();
            expect(screen.getByText(/You're set up with free model credits/)).toBeInTheDocument();
            expect(screen.getByRole("checkbox")).toBeDisabled();
            expect(screen.getByLabelText("Loading the agreement")).toBeInTheDocument();
            expect(ownModelButton()).toBeDisabled();
            expect(getStartedButton()).toBeDisabled();
        });

        test("names every document of the one clickwrap in SpotDraft's wording, as links to them, unticked", async () => {
            renderGate();
            await toTheWelcome();

            expect(screen.getAllByRole("checkbox")).toHaveLength(1);
            expect(await agreementBox()).not.toBeChecked();
            expect(await agreementBox()).toHaveAccessibleName("I agree to End User License Agreement and LLM Credits Terms.");
            const eula = screen.getByRole("link", { name: "End User License Agreement" });
            expect(eula).toHaveAttribute("href", EULA_DOCUMENT.url);
            expect(eula).toHaveAttribute("target", "_blank");
            expect(screen.getByRole("link", { name: "LLM Credits Terms" })).toHaveAttribute("href", CREDITS_DOCUMENT.url);
        });

        test("falls back to its own wording when SpotDraft's has no agreement list to fill", async () => {
            vi.mocked(service.getTerms).mockResolvedValue({ ...notAgreed, checkboxText: "" });
            renderGate();
            await toTheWelcome();

            expect(screen.getByRole("checkbox", { name: "I agree to the Solace Agent Mesh End User License Agreement" })).toBeInTheDocument();
            expect(screen.getByRole("link", { name: "Solace Agent Mesh End User License Agreement" })).toHaveAttribute("href", EULA_DOCUMENT.url);
        });

        test("one tick enables both Add My Own Model and Get Started, and unticking disables both", async () => {
            renderGate();
            await toTheWelcome();
            expect(ownModelButton()).toBeDisabled();
            expect(getStartedButton()).toBeDisabled();

            await tick();
            expect(ownModelButton()).toBeEnabled();
            expect(getStartedButton()).toBeEnabled();

            await tick();
            expect(ownModelButton()).toBeDisabled();
            expect(getStartedButton()).toBeDisabled();
            expect(screen.queryByRole("alert")).not.toBeInTheDocument();
        });

        test("getting started once ticked activates on the free model", async () => {
            vi.mocked(service.accept).mockResolvedValue(activeOnFreeModel);
            renderGate();
            await toTheWelcome();

            await tick();
            await userEvent.click(getStartedButton());

            await waitFor(() => expect(service.accept).toHaveBeenCalledWith({ freeModel: true, openedDocumentIds: [] }));
            await waitFor(() => expect(service.setUpFreeModels).toHaveBeenCalledWith(freeModel, true));
            expect(await screen.findByText("The app")).toBeInTheDocument();
        });

        test("the documents the person opened are recorded as opened, and opening one ticks nothing", async () => {
            vi.mocked(service.accept).mockResolvedValue(activeOnFreeModel);
            renderGate();
            await toTheWelcome();

            fireEvent.click(screen.getByRole("link", { name: "LLM Credits Terms" }));
            fireEvent.click(screen.getByRole("link", { name: "LLM Credits Terms" }));
            fireEvent.click(screen.getByRole("link", { name: "End User License Agreement" }));
            expect(await agreementBox()).not.toBeChecked();
            await tick();
            await userEvent.click(getStartedButton());

            await waitFor(() => expect(service.accept).toHaveBeenCalledWith({ freeModel: true, openedDocumentIds: [CREDITS_DOCUMENT.id, EULA_DOCUMENT.id] }));
        });

        test("with the agreement on record it offers only the setup choice, and still waits for a button", async () => {
            vi.mocked(service.getTerms).mockResolvedValue(agreedTerms);
            vi.mocked(service.accept).mockResolvedValue(activeOnFreeModel);
            renderGate();
            expect(await screen.findByRole("heading", { name: "Welcome to Solace Agent Mesh" })).toBeInTheDocument();

            await waitFor(() => expect(getStartedButton()).toBeEnabled());
            expect(ownModelButton()).toBeEnabled();
            expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
            expect(screen.queryByRole("group", { name: "License agreement" })).not.toBeInTheDocument();
            expect(service.accept).not.toHaveBeenCalled();

            await userEvent.click(getStartedButton());
            await waitFor(() => expect(service.accept).toHaveBeenCalledWith({ freeModel: true, openedDocumentIds: [] }));
        });

        test("a build without the free model whose license agreement is on record offers only the setup choice", async () => {
            vi.mocked(service.getActivation).mockResolvedValue({ ...eulaPending, freeModelAvailable: false, freeModel: undefined, agreements: { eulaClickwrapId: "eula-1" } });
            vi.mocked(service.getTerms).mockResolvedValue(agreedTerms);
            renderGate();

            await waitFor(() => expect(ownModelButton()).toBeEnabled());
            expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
            expect(screen.queryByText(/This account has already agreed/)).not.toBeInTheDocument();
        });

        test("terms SpotDraft can't load offer to load them again", async () => {
            vi.mocked(service.getTerms).mockRejectedValueOnce(apiError(502, "SpotDraft unreachable")).mockResolvedValue(notAgreed);
            renderGate();

            expect(await screen.findByText("Couldn't reach Solace to load the license agreement. Check your network connection and try again.")).toBeInTheDocument();
            expect(ownModelButton()).toBeDisabled();
            await userEvent.click(screen.getByRole("button", { name: "Try Again" }));

            await toTheWelcome();
            expect(service.getTerms).toHaveBeenCalledTimes(2);
            expect(screen.queryByText(/Couldn't reach Solace/)).not.toBeInTheDocument();
        });

        test("a build with no agreement linked says so and offers no retry", async () => {
            vi.mocked(service.getTerms).mockRejectedValue(apiError(503, "This build of Solace Agent Mesh has no license agreement linked, so it can't be set up."));
            renderGate();

            expect(await screen.findByText(/has no license agreement linked/)).toBeInTheDocument();
            expect(screen.queryByRole("button", { name: "Try Again" })).not.toBeInTheDocument();
            expect(ownModelButton()).toBeDisabled();
        });

        test("a build that links no clickwrap never asks for the terms", async () => {
            vi.mocked(service.getActivation).mockResolvedValue({ ...eulaPending, agreements: { eulaClickwrapId: "" } });
            renderGate();

            expect(await screen.findByText(/has no license agreement linked/)).toBeInTheDocument();
            expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
            expect(ownModelButton()).toBeDisabled();
            expect(service.getTerms).not.toHaveBeenCalled();
        });
    });

    test("choosing the free model over their own models confirms first, then activates", async () => {
        withModelsConfigured(true);
        vi.mocked(service.getActivation).mockResolvedValue(eulaPending);
        vi.mocked(service.accept).mockResolvedValue(activeOnFreeModel);
        renderGate();
        await toTheWelcome();

        await tick();
        await userEvent.click(getStartedButton());
        expect(screen.getByText(/will be replaced by the free model/)).toBeInTheDocument();
        expect(service.accept).not.toHaveBeenCalled();

        await userEvent.click(screen.getByRole("button", { name: "Replace and Get Started" }));

        await waitFor(() => expect(service.accept).toHaveBeenCalledWith({ freeModel: true, openedDocumentIds: [] }));
        await waitFor(() => expect(service.setUpFreeModels).toHaveBeenCalledWith(freeModel, true));
    });

    test("backing out of the replacement returns to the welcome, still ticked, and accepts nothing", async () => {
        withModelsConfigured(true);
        vi.mocked(service.getActivation).mockResolvedValue(eulaPending);
        renderGate();
        await toTheWelcome();

        await tick();
        await userEvent.click(getStartedButton());
        await userEvent.click(screen.getByRole("button", { name: "Back" }));

        expect(screen.getByRole("heading", { name: "Welcome to Solace Agent Mesh" })).toBeInTheDocument();
        expect(await agreementBox()).toBeChecked();
        expect(screen.getByRole("button", { name: "Keep Using My Models" })).toBeEnabled();
        expect(service.accept).not.toHaveBeenCalled();
    });

    test("the onboarding stays up until the free model's models exist", async () => {
        vi.mocked(service.getActivation).mockResolvedValue(eulaPending);
        vi.mocked(service.accept).mockResolvedValue(activeOnFreeModel);
        let modelsCreated: () => void = () => {};
        vi.mocked(service.setUpFreeModels).mockImplementation(() => new Promise(resolve => (modelsCreated = () => resolve())));
        renderGate();
        await toTheWelcome();

        await tick();
        await userEvent.click(getStartedButton());
        await waitFor(() => expect(service.setUpFreeModels).toHaveBeenCalledWith(freeModel, true));
        expect(screen.getByText("Setting things up…")).toBeInTheDocument();
        expect(screen.queryByText("The app")).not.toBeInTheDocument();

        act(() => modelsCreated());
        expect(await screen.findByText("The app")).toBeInTheDocument();
    });

    test("adding their own model accepts the license agreement alone and opens the model setup", async () => {
        vi.mocked(service.getActivation).mockResolvedValue(eulaPending);
        vi.mocked(service.accept).mockResolvedValue(active);
        renderGate();
        await toTheWelcome();

        await userEvent.click(await agreementBox());
        await userEvent.click(ownModelButton());

        expect(service.accept).toHaveBeenCalledWith({ freeModel: false, openedDocumentIds: [] });
        const setupShowing = await screen.findByRole("button", { name: "Model setup is showing" });
        expect(within(screen.getByTestId("activation-cover")).getByText("Setting things up…")).toBeInTheDocument();

        await userEvent.click(setupShowing);
        expect(screen.queryByTestId("activation-cover")).not.toBeInTheDocument();
    });

    test("someone who already has models keeps using them", async () => {
        withModelsConfigured(true);
        vi.mocked(service.getActivation).mockResolvedValue(eulaPending);
        vi.mocked(service.accept).mockResolvedValue(active);
        renderGate();
        await toTheWelcome();

        await userEvent.click(await agreementBox());
        await userEvent.click(screen.getByRole("button", { name: "Keep Using My Models" }));

        expect(service.accept).toHaveBeenCalledWith({ freeModel: false, openedDocumentIds: [] });
        expect(await screen.findByText("The app")).toBeInTheDocument();
    });

    test("a build without the free model offers only the license agreement and the person's own model", async () => {
        const ownOnly: Activation = { ...eulaPending, freeModelAvailable: false, freeModel: undefined, agreements: { eulaClickwrapId: "eula-1" } };
        vi.mocked(service.getActivation).mockResolvedValue(ownOnly);
        vi.mocked(service.accept).mockResolvedValue(active);
        renderGate();
        await toTheWelcome();

        expect(screen.getAllByRole("checkbox")).toHaveLength(1);
        expect(screen.queryByRole("button", { name: "Get Started" })).not.toBeInTheDocument();
        expect(screen.queryByText(/free model credits/)).not.toBeInTheDocument();
        await userEvent.click(await agreementBox());
        await userEvent.click(ownModelButton());

        expect(service.accept).toHaveBeenCalledWith({ freeModel: false, openedDocumentIds: [] });
    });

    describe("when the model setup never shows", () => {
        const flush = () => act(() => vi.advanceTimersByTimeAsync(0));

        async function chooseOwnModelWithFakeTimers(layoutMounts: boolean) {
            vi.useFakeTimers();
            vi.mocked(service.getActivation).mockResolvedValue(eulaPending);
            vi.mocked(service.accept).mockResolvedValue(active);
            renderGate({ layoutMounts });
            await act(() => vi.advanceTimersByTimeAsync(50));
            fireEvent.click(screen.getByRole("checkbox", { name: /End User License Agreement/ }));
            fireEvent.click(ownModelButton());
            await flush();
            await flush();
            expect(screen.getByTestId("activation-cover")).toBeInTheDocument();
        }

        afterEach(() => vi.useRealTimers());

        test("the cover lifts 3 s after the app's layout is up", async () => {
            await chooseOwnModelWithFakeTimers(true);

            await act(() => vi.advanceTimersByTimeAsync(2_999));
            expect(screen.getByTestId("activation-cover")).toBeInTheDocument();
            await act(() => vi.advanceTimersByTimeAsync(1));
            expect(screen.queryByTestId("activation-cover")).not.toBeInTheDocument();
        });

        test("the cover waits for a slow layout, up to 15 s", async () => {
            await chooseOwnModelWithFakeTimers(false);

            await act(() => vi.advanceTimersByTimeAsync(14_999));
            expect(screen.getByTestId("activation-cover")).toBeInTheDocument();
            await act(() => vi.advanceTimersByTimeAsync(1));
            expect(screen.queryByTestId("activation-cover")).not.toBeInTheDocument();
        });
    });

    test("an agreement SpotDraft couldn't record says so, and clicking again tries again", async () => {
        vi.mocked(service.getActivation).mockResolvedValue(eulaPending);
        vi.mocked(service.accept).mockRejectedValueOnce(apiError(502, "Solace Agent Mesh couldn't record your agreement with Solace.")).mockResolvedValue(activeOnFreeModel);
        renderGate();
        await toTheWelcome();

        await tick();
        await userEvent.click(getStartedButton());

        expect(await screen.findByText("Solace Agent Mesh couldn't record your agreement with Solace.")).toBeInTheDocument();
        expect(screen.queryByText("The app")).not.toBeInTheDocument();
        await waitFor(() => expect(getStartedButton()).toBeEnabled());
        await userEvent.click(getStartedButton());

        expect(await screen.findByText("The app")).toBeInTheDocument();
        expect(service.accept).toHaveBeenCalledTimes(2);
    });

    test("a free model setup that fails after the acceptance keeps the onboarding up and can be tried again", async () => {
        vi.mocked(service.getActivation).mockImplementation(async () => (vi.mocked(service.accept).mock.calls.length ? activeOnFreeModel : eulaPending));
        vi.mocked(service.accept).mockResolvedValue(activeOnFreeModel);
        vi.mocked(service.setUpFreeModels).mockRejectedValueOnce(apiError(500, "models down")).mockResolvedValue();
        renderGate();
        await toTheWelcome();

        await tick();
        await userEvent.click(getStartedButton());

        expect(await screen.findByText("Solace Agent Mesh couldn't set up the free model. Try again.")).toBeInTheDocument();
        expect(screen.queryByText("The app")).not.toBeInTheDocument();
        await waitFor(() => expect(getStartedButton()).toBeEnabled());
        await userEvent.click(getStartedButton());

        expect(await screen.findByText("The app")).toBeInTheDocument();
        const replacingSetups = vi.mocked(service.setUpFreeModels).mock.calls.filter(([, replace]) => replace);
        expect(replacingSetups).toHaveLength(2);
    });

    test("a start-up setup of the free model's models that fails is tried again", async () => {
        vi.mocked(service.getActivation).mockResolvedValue(activeOnFreeModel);
        vi.mocked(service.setUpFreeModels).mockRejectedValueOnce(new Error("down")).mockResolvedValue();
        renderGate();

        await waitFor(() => expect(service.setUpFreeModels).toHaveBeenCalledTimes(2), { timeout: 3_000 });
    });

    test("a failed choice of their own model asks for no model setup", async () => {
        vi.mocked(service.getActivation).mockResolvedValue(eulaPending);
        vi.mocked(service.accept).mockRejectedValueOnce(apiError(502, "Solace Agent Mesh couldn't record your agreement with Solace.")).mockResolvedValue(activeOnFreeModel);
        renderGate();
        await toTheWelcome();

        await userEvent.click(await agreementBox());
        await userEvent.click(ownModelButton());
        expect(await screen.findByText("Solace Agent Mesh couldn't record your agreement with Solace.")).toBeInTheDocument();

        await waitFor(() => expect(getStartedButton()).toBeEnabled());
        await userEvent.click(getStartedButton());

        expect(await screen.findByText("The app")).toBeInTheDocument();
        expect(screen.queryByTestId("activation-cover")).not.toBeInTheDocument();
    });

    test("a failed check of the person's models offers to check again before the choice can be made", async () => {
        vi.mocked(service.getModelsConfigured).mockRejectedValueOnce(new Error("down")).mockRejectedValueOnce(new Error("down")).mockResolvedValue(true);
        vi.mocked(service.getActivation).mockResolvedValue(eulaPending);
        renderGate();

        expect(await screen.findByText("Solace Agent Mesh couldn't check your models.", {}, { timeout: 5_000 })).toBeInTheDocument();
        expect(getStartedButton()).toBeDisabled();
        await userEvent.click(screen.getByRole("button", { name: "Try Again" }));

        expect(await screen.findByRole("button", { name: "Keep Using My Models" })).toBeInTheDocument();
        await userEvent.click(await agreementBox());
        expect(screen.getByRole("button", { name: "Keep Using My Models" })).toBeEnabled();
    });

    test("a failed sign-in explains itself and stays on the sign-in", async () => {
        const denied: Activation = { ...signedOut, signInFailure: { code: "denied", message: "Solace ID did not complete the sign-in. Try again." } };
        vi.mocked(service.getActivation).mockResolvedValueOnce(signedOut).mockResolvedValue(denied);
        vi.mocked(service.signIn).mockResolvedValue(signingIn);
        renderGate();

        await userEvent.click(await screen.findByRole("button", { name: "Sign In" }));

        expect(await screen.findByText("Solace ID did not complete the sign-in. Try again.", {}, { timeout: 5_000 })).toBeInTheDocument();
        expect(screen.queryByRole("status")).not.toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Sign In" })).toBeEnabled();
    });

    test("a sign-in that cannot start explains itself", async () => {
        vi.mocked(service.getActivation).mockResolvedValue(signedOut);
        vi.mocked(service.signIn).mockRejectedValue(apiError(503, "Solace ID sign-in is not available in this build."));
        const track = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
        renderGate();

        await userEvent.click(await screen.findByRole("button", { name: "Sign In" }));

        expect(await screen.findByText("Solace ID sign-in is not available in this build.")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Sign In" })).toBeEnabled();
        expect(track).toHaveBeenCalledWith("activation:sign_in_failed", { signInFailure: "unavailable" });
    });

    test("a sign-in request lost on the way keeps following the sign-in still open in the browser", async () => {
        vi.mocked(service.getActivation)
            .mockResolvedValueOnce(signedOut)
            .mockResolvedValueOnce({ ...signedOut, signingIn: true })
            .mockResolvedValue(eulaPending);
        vi.mocked(service.signIn).mockRejectedValue(new TypeError("Load failed"));
        const track = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
        renderGate();

        await userEvent.click(await screen.findByRole("button", { name: "Sign In" }));

        expect(await screen.findByText(/lost track of the sign-in/)).toBeInTheDocument();
        expect(screen.queryByText("Load failed")).not.toBeInTheDocument();
        expect(track).toHaveBeenCalledWith("activation:sign_in_failed", { signInFailure: "request_failed" });
        expect(await screen.findByRole("heading", { name: "Welcome to Solace Agent Mesh" }, { timeout: 5_000 })).toBeInTheDocument();
    });

    test("coming back to the app while the browser is open allows a fresh sign-in", async () => {
        vi.mocked(service.getActivation).mockImplementation(async () => {
            const attempts = vi.mocked(service.signIn).mock.calls.length;
            if (attempts === 0) return signedOut;
            return attempts === 1 ? signingIn : eulaPending;
        });
        vi.mocked(service.signIn).mockResolvedValue(signingIn);
        vi.mocked(service.cancelSignIn).mockResolvedValue(signedOut);
        renderGate();

        await userEvent.click(await screen.findByRole("button", { name: "Sign In" }));
        const signInAgain = await screen.findByRole("button", { name: "Sign In Again" });
        expect(signInAgain).toBeDisabled();
        expect(screen.getByRole("status")).toHaveTextContent("Finish signing in with Solace ID in your browser.");

        await act(() => new Promise(resolve => setTimeout(resolve, 5)));
        fireEvent.focus(window);
        await waitFor(() => expect(signInAgain).toBeEnabled());
        await userEvent.click(signInAgain);

        await waitFor(() => expect(service.cancelSignIn).toHaveBeenCalledTimes(1));
        expect(service.signIn).toHaveBeenCalledTimes(2);
        expect(await screen.findByRole("heading", { name: "Welcome to Solace Agent Mesh" }, { timeout: 5_000 })).toBeInTheDocument();
    });

    test("a poll that fails during a sign-in keeps the sign-in on screen", async () => {
        vi.mocked(service.getActivation)
            .mockResolvedValueOnce(signedOut)
            .mockRejectedValueOnce(new Error("down"))
            .mockRejectedValueOnce(new Error("down"))
            .mockRejectedValueOnce(new Error("down"))
            .mockReturnValue(new Promise<Activation>(() => {}));
        vi.mocked(service.signIn).mockResolvedValue(signingIn);
        renderGate();

        await userEvent.click(await screen.findByRole("button", { name: "Sign In" }));
        await waitFor(() => expect(service.getActivation).toHaveBeenCalledTimes(5), { timeout: 12_000 });

        expect(screen.queryByText("Solace Agent Mesh could not start")).not.toBeInTheDocument();
        expect(screen.getByRole("status")).toHaveTextContent("Finish signing in with Solace ID in your browser.");
    }, 20_000);

    test("an install that cannot be reached offers a retry", async () => {
        vi.mocked(service.getActivation).mockRejectedValueOnce(new Error("down")).mockRejectedValueOnce(new Error("down")).mockRejectedValueOnce(new Error("down")).mockResolvedValue(active);
        renderGate();

        await userEvent.click(await screen.findByRole("button", { name: "Try Again" }, { timeout: 8_000 }));

        expect(await screen.findByText("The app")).toBeInTheDocument();
    }, 15_000);

    describe("product analytics", () => {
        let reported: string[];
        beforeEach(() => {
            reported = [];
            vi.spyOn(productAnalyticsClient, "track").mockImplementation(event => void reported.push(event));
            vi.spyOn(productAnalyticsClient, "flushNow").mockImplementation(async () => void reported.push("flushed"));
        });

        test("reports the Sign In click and the sign-in that succeeded", async () => {
            vi.mocked(service.getActivation).mockResolvedValueOnce(signedOut).mockResolvedValue(eulaPending);
            vi.mocked(service.signIn).mockResolvedValue(signingIn);
            renderGate();

            await userEvent.click(await screen.findByRole("button", { name: "Sign In" }));
            expect(await screen.findByRole("heading", { name: "Welcome to Solace Agent Mesh" }, { timeout: 5_000 })).toBeInTheDocument();

            expect(reported.filter(event => event.startsWith("activation:sign"))).toEqual(["activation:sign_in_clicked", "activation:signed_in"]);
        });

        test("reports why a sign-in failed by its code alone", async () => {
            const failed: Activation = { ...signedOut, signInFailure: { code: "denied", message: "Solace ID did not complete the sign-in for ada@example.com." } };
            vi.mocked(service.getActivation).mockResolvedValueOnce(signedOut).mockResolvedValue(failed);
            vi.mocked(service.signIn).mockResolvedValue(signingIn);
            renderGate();

            await userEvent.click(await screen.findByRole("button", { name: "Sign In" }));

            await waitFor(() => expect(productAnalyticsClient.track).toHaveBeenCalledWith("activation:sign_in_failed", { signInFailure: "denied" }), { timeout: 5_000 });
        });

        test("sends the steps before the acceptance ahead of it, and reports the acceptance after", async () => {
            vi.mocked(service.getActivation).mockResolvedValue(eulaPending);
            vi.mocked(service.accept).mockImplementation(async () => {
                reported.push("accept");
                return activeOnFreeModel;
            });
            renderGate();

            await toTheWelcome();
            await tick();
            await userEvent.click(getStartedButton());
            expect(await screen.findByText("The app")).toBeInTheDocument();

            expect(reported).toEqual(["activation:terms_shown", "activation:setup_chosen", "flushed", "accept", "activation:accepted"]);
            expect(productAnalyticsClient.track).toHaveBeenCalledWith("activation:terms_shown", { alreadyAgreed: false });
            expect(productAnalyticsClient.track).toHaveBeenCalledWith("activation:setup_chosen", { setupPath: "free_model" });
            expect(productAnalyticsClient.track).toHaveBeenCalledWith("activation:accepted", { setupPath: "free_model" });
        });

        test("reports the terms SpotDraft couldn't load, and the terms once they show", async () => {
            vi.mocked(service.getActivation).mockResolvedValue(eulaPending);
            vi.mocked(service.getTerms).mockRejectedValueOnce(apiError(502, "SpotDraft unreachable")).mockResolvedValue(notAgreed);
            renderGate();

            await userEvent.click(await screen.findByRole("button", { name: "Try Again" }));
            await toTheWelcome();

            expect(reported).toEqual(["activation:terms_error", "activation:terms_shown"]);
        });

        test("reports an agreement SpotDraft couldn't record as a failed acceptance on the chosen path", async () => {
            vi.mocked(service.getActivation).mockResolvedValue(eulaPending);
            vi.mocked(service.accept).mockRejectedValue(apiError(502, "Solace Agent Mesh couldn't record your agreement. Try again."));
            renderGate();

            await toTheWelcome();
            await userEvent.click(await agreementBox());
            await userEvent.click(ownModelButton());

            await waitFor(() => expect(productAnalyticsClient.track).toHaveBeenCalledWith("activation:accept_failed", { setupPath: "own_model" }));
            expect(productAnalyticsClient.track).not.toHaveBeenCalledWith("activation:accepted", expect.anything());
        });

        test("reports terms the account agreed to on an earlier install as already agreed", async () => {
            vi.mocked(service.getActivation).mockResolvedValue(eulaPending);
            vi.mocked(service.getTerms).mockResolvedValue(agreedTerms);
            renderGate();

            expect(await screen.findByRole("heading", { name: "Welcome to Solace Agent Mesh" })).toBeInTheDocument();
            await waitFor(() => expect(getStartedButton()).toBeEnabled());

            expect(productAnalyticsClient.track).toHaveBeenCalledWith("activation:terms_shown", { alreadyAgreed: true });
        });

        test("keeps the first answer when a failed acceptance reloads terms SpotDraft now has as agreed", async () => {
            vi.mocked(service.getActivation).mockResolvedValue(eulaPending);
            vi.mocked(service.getTerms).mockResolvedValueOnce(notAgreed).mockResolvedValue(agreedTerms);
            vi.mocked(service.accept).mockRejectedValue(apiError(502, "Solace Agent Mesh couldn't record your agreement. Try again."));
            renderGate();

            await toTheWelcome();
            await userEvent.click(await agreementBox());
            await userEvent.click(ownModelButton());
            await waitFor(() => expect(productAnalyticsClient.track).toHaveBeenCalledWith("activation:accept_failed", { setupPath: "own_model" }));
            await waitFor(() => expect(service.getTerms).toHaveBeenCalledTimes(2));

            expect(vi.mocked(productAnalyticsClient.track).mock.calls.filter(([event]) => event === "activation:terms_shown")).toEqual([["activation:terms_shown", { alreadyAgreed: false }]]);
        });
    });
});
