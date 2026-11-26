// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { Activation, FreeModelStatus } from "@/api/activation";
import * as service from "@/api/activation/service";
import { useModelConfigs } from "@/api/models";
import type { ModelConfig } from "@/api/models/types";

import { productAnalyticsClient } from "@/common/product-analytics/client";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { FreeCreditsBanner } from "./FreeCreditsBanner";
import { FreeCreditsNavItem } from "./FreeCreditsNavItem";
import { FreeModelErrorActions } from "./FreeModelErrorActions";
import { FreeModelManagedNotice } from "./FreeModelManagedNotice";
import { FreeModelName } from "./FreeModelName";
import { ModelConfigIcon } from "./ModelConfigIcon";
import { useFreeModel } from "./useFreeModel";

vi.mock("@/api/activation/service", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/activation/service")>()),
    getActivation: vi.fn(),
    getFreeModel: vi.fn(),
    signInFreeModel: vi.fn(),
    cancelSignIn: vi.fn(),
    refreshFreeModel: vi.fn(),
}));

vi.mock("@/api/models", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/models")>()),
    useModelConfigs: vi.fn(),
}));

vi.mock("@/common/dialogs/ModelSetupDialog", () => ({
    ModelSetupDialog: ({ open, setupCopy }: { open: boolean; setupCopy?: { title: string } }) => (open ? <div role="dialog">{setupCopy?.title}</div> : null),
}));

const freeModel = { grantId: "1c58a105-c275-4401-b9af-9b1af81a80af", provider: "openai", apiBase: "https://llm.example.test/v1", generalModelName: "free", planningModelName: "free-planning" };
const onFreeModel: Activation = {
    state: "active",
    signingIn: false,
    account: { subject: "auth0|ada", email: "ada@example.com", name: "Ada" },
    agreements: { eulaClickwrapId: "eula-1" },
    freeModelAvailable: true,
    freeModel,
    freeModelChosen: true,
};

const freeRow = (alias: string) =>
    ({ id: `${alias}-id`, alias, provider: "openai", modelName: alias === "planning" ? "free-planning" : "free", apiBase: freeModel.apiBase, authConfig: { type: "oauth2_delegated", grant_id: freeModel.grantId } }) as unknown as ModelConfig;
const ownRow = (alias: string) => ({ id: `${alias}-id`, alias, provider: "anthropic", modelName: "claude", apiBase: "https://api.anthropic.com", authConfig: { type: "apikey" } }) as unknown as ModelConfig;

function withModels(models: ModelConfig[]) {
    vi.mocked(useModelConfigs).mockReturnValue({ data: models, isLoading: false, error: null } as unknown as ReturnType<typeof useModelConfigs>);
}

function withFreeModel(status: FreeModelStatus) {
    vi.mocked(service.getFreeModel).mockResolvedValue(status);
}

function renderOnFreeDesktop(ui: React.ReactElement) {
    return renderWithProviders(ui, { configContextValues: { activationGateEnabled: true }, chatContextValues: { hasModelConfigWrite: true } });
}

describe("the free model's credits banner", () => {
    beforeEach(() => {
        vi.mocked(service.getActivation).mockResolvedValue(onFreeModel);
        withModels([freeRow("general"), freeRow("planning")]);
    });
    afterEach(() => vi.resetAllMocks());

    test("shows how much is used, with the way onto the person's own model", async () => {
        withFreeModel({ status: "available", remainingPercent: 72.4, models: [] });
        renderOnFreeDesktop(<FreeCreditsBanner />);

        expect(await screen.findByRole("heading", { name: "28% of free credits used" })).toBeInTheDocument();
        const meter = screen.getByRole("meter", { name: "Free credits used" });
        expect(meter).toHaveAttribute("value", "28");
        expect(meter).toHaveAttribute("aria-valuetext", "28% used");
        expect(screen.getByText(/come with the Solace ID\. Add a model to replace them anytime\./)).toBeInTheDocument();
        expect(screen.queryByRole("link", { name: "Contact Solace" })).not.toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: "Replace With My Own Model" }));
        expect(screen.getByRole("dialog")).toHaveTextContent("Replace the Free Model");
    });

    test("warns once 80% of the credits are used", async () => {
        withFreeModel({ status: "available", remainingPercent: 20, models: [] });
        renderOnFreeDesktop(<FreeCreditsBanner />);

        expect(await screen.findByRole("heading", { name: "80% of free credits used" })).toBeInTheDocument();
        expect(screen.getByText("Most free credits are used. Add a model before the credits run out to keep agents working.")).toBeInTheDocument();
    });

    test("says when no credits remain, and offers a way to contact Solace", async () => {
        withFreeModel({ status: "available", remainingPercent: 0, models: [] });
        renderOnFreeDesktop(<FreeCreditsBanner />);

        expect(await screen.findByRole("heading", { name: "No free credits left" })).toBeInTheDocument();
        expect(screen.queryByRole("meter")).not.toBeInTheDocument();
        expect(screen.getByRole("link", { name: "Contact Solace" })).toHaveAttribute("href", "https://solace.com/contact/");
    });

    test.each([
        [{ status: "available", remainingPercent: null }, "Free credits"],
        [{ status: "paused", remainingPercent: null }, "The free model is paused"],
        [{ status: "unavailable", remainingPercent: null }, "Free credits"],
    ] as const)("tells %o as %s", async (status, title) => {
        withFreeModel({ ...status, models: [] });
        renderOnFreeDesktop(<FreeCreditsBanner />);

        expect(await screen.findByRole("heading", { name: title })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Replace With My Own Model" })).toBeInTheDocument();
    });

    test("offers to sign in again once the free model's sign-in expired", async () => {
        withFreeModel({ status: "reauthorization_required", remainingPercent: null, models: [] });
        vi.mocked(service.signInFreeModel).mockResolvedValue({ ...onFreeModel, signingIn: true });
        renderOnFreeDesktop(<FreeCreditsBanner />);

        await userEvent.click(await screen.findByRole("button", { name: "Sign In Again" }));

        expect(service.signInFreeModel).toHaveBeenCalledTimes(1);
        await waitFor(() => expect(screen.getByRole("button", { name: "Sign In Again" })).toBeDisabled());
    });

    test("shows nothing once the person's own models replaced the free one", async () => {
        withModels([ownRow("general"), ownRow("planning")]);
        withFreeModel({ status: "available", remainingPercent: 50, models: [] });
        renderOnFreeDesktop(<FreeCreditsBanner />);

        await waitFor(() => expect(service.getActivation).toHaveBeenCalled());
        expect(screen.queryByTestId("freeCreditsBanner")).not.toBeInTheDocument();
        expect(service.getFreeModel).not.toHaveBeenCalled();
    });

    test("shows nothing on an install that chose its own model", async () => {
        vi.mocked(service.getActivation).mockResolvedValue({ ...onFreeModel, freeModelChosen: false });
        renderOnFreeDesktop(<FreeCreditsBanner />);

        await waitFor(() => expect(service.getActivation).toHaveBeenCalled());
        expect(screen.queryByTestId("freeCreditsBanner")).not.toBeInTheDocument();
    });

    test("shows nothing in a build without the gate", () => {
        renderWithProviders(<FreeCreditsBanner />, { configContextValues: { activationGateEnabled: false } });
        expect(screen.queryByTestId("freeCreditsBanner")).not.toBeInTheDocument();
        expect(service.getActivation).not.toHaveBeenCalled();
    });
});

describe("the free credits funnel events", () => {
    const CREDITS_EVENTS = ["free_model:credits_low", "free_model:credits_out"];

    beforeEach(() => {
        vi.mocked(service.getActivation).mockResolvedValue(onFreeModel);
        withModels([freeRow("general"), freeRow("planning")]);
        vi.spyOn(productAnalyticsClient, "trackOnce").mockImplementation(() => {});
        vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
    });
    afterEach(() => {
        vi.restoreAllMocks();
        vi.resetAllMocks();
    });

    function renderBannerAndRail() {
        renderOnFreeDesktop(
            <>
                <FreeCreditsBanner />
                <FreeCreditsNavItem collapsed={false} />
            </>
        );
    }

    function creditsEventsReported(spy: typeof productAnalyticsClient.track) {
        return vi
            .mocked(spy)
            .mock.calls.map(([event]) => event)
            .filter(event => CREDITS_EVENTS.includes(event));
    }

    test.each([
        [20, "80% of free credits used", "free_model:credits_low"],
        [1, "99% of free credits used", "free_model:credits_low"],
        [0, "No free credits left", "free_model:credits_out"],
    ])("with %d%% remaining, the banner and the rail report one event through the once-per-load path", async (remainingPercent, title, event) => {
        withFreeModel({ status: "available", remainingPercent, models: [] });
        renderBannerAndRail();

        expect(await screen.findByRole("heading", { name: title })).toBeInTheDocument();
        await waitFor(() => expect(creditsEventsReported(productAnalyticsClient.trackOnce)).toEqual([event, event]));
        expect(creditsEventsReported(productAnalyticsClient.track)).toEqual([]);
    });

    test.each([
        [{ status: "available", remainingPercent: 50 }, "50% of free credits used"],
        [{ status: "reauthorization_required", remainingPercent: null }, "Sign in again to keep using free credits"],
        [{ status: "paused", remainingPercent: null }, "The free model is paused"],
        [{ status: "unavailable", remainingPercent: null }, "Free credits"],
    ] as const)("reports no credits event for %o", async (status, title) => {
        withFreeModel({ ...status, models: [] });
        renderBannerAndRail();

        expect(await screen.findByRole("heading", { name: title })).toBeInTheDocument();
        expect(creditsEventsReported(productAnalyticsClient.trackOnce)).toEqual([]);
        expect(creditsEventsReported(productAnalyticsClient.track)).toEqual([]);
    });
});

describe("signing in again for the free model", () => {
    beforeEach(() => {
        withModels([freeRow("general"), freeRow("planning")]);
        withFreeModel({ status: "reauthorization_required", remainingPercent: null, models: [] });
    });
    afterEach(() => vi.resetAllMocks());

    test("reports a sign-in by another account", async () => {
        vi.mocked(service.getActivation)
            .mockResolvedValueOnce(onFreeModel)
            .mockResolvedValue({ ...onFreeModel, signInFailure: { code: "different_account", message: "Sign in with the Solace ID account you first set up Solace Agent Mesh with." } });
        vi.mocked(service.signInFreeModel).mockResolvedValue({ ...onFreeModel, signingIn: true });
        renderOnFreeDesktop(<FreeModelErrorActions message="Error: The sign-in this model uses has expired. Sign in again to keep using it." />);

        await userEvent.click(await screen.findByRole("button", { name: "Sign In Again" }));

        expect(await screen.findByText(/the Solace ID account you first set up/, {}, { timeout: 5_000 })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Sign In Again" })).toBeEnabled();
    }, 15_000);

    test("says so once the new sign-in is saved, and reads the credits again", async () => {
        vi.mocked(service.getActivation).mockResolvedValue(onFreeModel);
        vi.mocked(service.signInFreeModel).mockResolvedValue({ ...onFreeModel, signingIn: true });
        renderOnFreeDesktop(
            <>
                <FreeCreditsBanner />
                <FreeModelErrorActions message="Error: The sign-in this model uses has expired. Sign in again to keep using it." />
            </>
        );
        await waitFor(() => expect(screen.getAllByRole("button", { name: "Sign In Again" })).toHaveLength(2));
        const underTheError = screen.getAllByRole("button", { name: "Sign In Again" })[1];
        withFreeModel({ status: "available", remainingPercent: 60, models: [] });

        await userEvent.click(underTheError);

        expect(await screen.findByText(/Signed in again\. Send the message/, {}, { timeout: 5_000 })).toBeInTheDocument();
        expect(await screen.findByRole("heading", { name: "40% of free credits used" })).toBeInTheDocument();
    }, 15_000);

    test("a request that cannot start the sign-in explains itself", async () => {
        vi.mocked(service.getActivation).mockResolvedValue(onFreeModel);
        vi.mocked(service.signInFreeModel).mockRejectedValue(Object.assign(new Error("Another program may be using the sign-in ports."), { status: 503 }));
        renderOnFreeDesktop(<FreeModelErrorActions message="Error: The sign-in this model uses has expired. Sign in again to keep using it." />);

        await userEvent.click(await screen.findByRole("button", { name: "Sign In Again" }));

        expect(await screen.findByText("Another program may be using the sign-in ports.")).toBeInTheDocument();
    });

    test("a second try while the browser is still open starts the sign-in afresh", async () => {
        vi.mocked(service.getActivation).mockResolvedValue({ ...onFreeModel, signingIn: true });
        vi.mocked(service.signInFreeModel).mockResolvedValue({ ...onFreeModel, signingIn: true });
        vi.mocked(service.cancelSignIn).mockResolvedValue(onFreeModel);
        renderOnFreeDesktop(<FreeModelErrorActions message="Error: The sign-in this model uses has expired. Sign in again to keep using it." />);

        await userEvent.click(await screen.findByRole("button", { name: "Sign In Again" }));
        await act(() => new Promise(resolve => setTimeout(resolve, 5)));
        act(() => {
            window.dispatchEvent(new Event("focus"));
        });
        await waitFor(() => expect(screen.getByRole("button", { name: "Sign In Again" })).toBeEnabled());
        await userEvent.click(screen.getByRole("button", { name: "Sign In Again" }));

        await waitFor(() => expect(service.cancelSignIn).toHaveBeenCalledTimes(1));
        expect(service.signInFreeModel).toHaveBeenCalledTimes(2);
    });
});

describe("the way forward under a chat error", () => {
    beforeEach(() => {
        vi.mocked(service.getActivation).mockResolvedValue(onFreeModel);
        withModels([freeRow("general"), freeRow("planning")]);
    });
    afterEach(() => vi.resetAllMocks());

    test.each([["Error: The free model's allowance is used up. Add a model to replace it."], ["Error: The free model is paused. Add a model to replace it."]])("offers the person's own model under %s", async message => {
        renderOnFreeDesktop(<FreeModelErrorActions message={message} />);
        expect(await screen.findByRole("button", { name: "Replace With My Own Model" })).toBeInTheDocument();
    });

    test("offers the free model's terms under their refusal", async () => {
        renderOnFreeDesktop(<FreeModelErrorActions message="Error: Terms not accepted. Accept the free model's terms to continue." />);
        expect(await screen.findByRole("button", { name: "Review Terms" })).toBeInTheDocument();
    });

    test("offers nothing under another error, or for a sign-in service that can't be reached", async () => {
        renderOnFreeDesktop(
            <>
                <FreeModelErrorActions message="Error: The model provider is unreachable." />
                <FreeModelErrorActions message="Error: This model's sign-in service can't be reached right now. Try again in a few minutes." />
            </>
        );
        await waitFor(() => expect(service.getActivation).toHaveBeenCalled());
        expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });

    test("offers no switch once the free model is no longer in use", async () => {
        withModels([ownRow("general"), ownRow("planning")]);
        renderOnFreeDesktop(<FreeModelErrorActions message="Error: The free model's allowance is used up. Add a model to replace it." />);
        await waitFor(() => expect(service.getActivation).toHaveBeenCalled());
        expect(screen.queryByRole("button", { name: "Replace With My Own Model" })).not.toBeInTheDocument();
    });

    test("offers nothing on an install that chose its own model", async () => {
        vi.mocked(service.getActivation).mockResolvedValue({ ...onFreeModel, freeModelChosen: false });
        renderOnFreeDesktop(<FreeModelErrorActions message="Error: The sign-in this model uses has expired. Sign in again to keep using it." />);
        await waitFor(() => expect(service.getActivation).toHaveBeenCalled());
        expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });
});

test("the free model's edit page says who manages it and offers the person's own model", async () => {
    vi.mocked(service.getActivation).mockResolvedValue(onFreeModel);
    withModels([freeRow("general"), freeRow("planning")]);
    renderOnFreeDesktop(<FreeModelManagedNotice />);
    const notice = screen.getByRole("region", { name: "This model is managed by Solace Agent Mesh" });
    await userEvent.click(within(notice).getByRole("button", { name: "Replace With My Own Model" }));
    expect(screen.getByRole("dialog")).toHaveTextContent("Replace the Free Model");
});

function ModelLabels({ models }: Readonly<{ models: ModelConfig[] }>) {
    const { freeModelLabel } = useFreeModel();
    return (
        <ul>
            {models.map(model => {
                const label = freeModelLabel(model);
                return (
                    <li key={model.id}>
                        {label ? <FreeModelName label={label} /> : model.modelName} · {label?.providerName ?? model.provider}
                    </li>
                );
            })}
        </ul>
    );
}

describe("how a free model is named", () => {
    beforeEach(() => vi.mocked(service.getActivation).mockResolvedValue(onFreeModel));
    afterEach(() => vi.resetAllMocks());

    test("as the free LLM says, from Anthropic and credited to Claude", async () => {
        const models = [freeRow("general"), freeRow("planning"), ownRow("mine")];
        withModels(models);
        withFreeModel({
            status: "available",
            remainingPercent: 90,
            models: [
                { modelName: "free", displayName: "Sonnet 5.5" },
                { modelName: "free-planning", displayName: "Opus 5.5" },
            ],
        });

        renderOnFreeDesktop(<ModelLabels models={models} />);

        const [general, planning, mine] = await waitFor(() => {
            const items = screen.getAllByRole("listitem");
            expect(items[0]).toHaveTextContent("Sonnet 5.5");
            return items;
        });
        expect(general).toHaveTextContent("Sonnet 5.5Powered byClaude · Anthropic");
        expect(planning).toHaveTextContent("Opus 5.5Powered byClaude · Anthropic");
        expect(mine).toHaveTextContent("claude · anthropic");
    });

    test("as the free model until the free LLM names it", async () => {
        withModels([freeRow("general")]);
        withFreeModel({ status: "available", remainingPercent: 90, models: [] });

        renderOnFreeDesktop(<ModelLabels models={[freeRow("general")]} />);

        await waitFor(() => expect(screen.getByRole("listitem")).toHaveTextContent("Free modelPowered byClaude · Anthropic"));
    });
});

describe("a saved model's icon", () => {
    beforeEach(() => vi.mocked(service.getActivation).mockResolvedValue(onFreeModel));
    afterEach(() => vi.resetAllMocks());

    test("shows the free model as Anthropic, though it is saved with an OpenAI-compatible provider", async () => {
        const openaiRow = { ...ownRow("mine"), provider: "openai" } as ModelConfig;
        withModels([freeRow("general"), openaiRow]);
        withFreeModel({ status: "available", remainingPercent: 90, models: [] });

        renderOnFreeDesktop(
            <>
                <ModelConfigIcon model={freeRow("general")} />
                <ModelConfigIcon model={openaiRow} />
            </>
        );

        expect(await screen.findByRole("img", { name: "anthropic" })).toBeInTheDocument();
        expect(screen.getByRole("img", { name: "openai" })).toBeInTheDocument();
    });

    test("falls back to the stored provider when the model is no longer saved", () => {
        withModels([]);

        renderOnFreeDesktop(<ModelConfigIcon model={undefined} storedProvider="openai" />);

        expect(screen.getByRole("img", { name: "openai" })).toBeInTheDocument();
    });
});
