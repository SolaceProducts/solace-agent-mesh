// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { UseFormReturn } from "react-hook-form";
import { describe, expect, test } from "vitest";

import type { RemoteAgentResponse } from "@/api/remote-agents";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import type { AdditionalInfoFormData } from "./AdditionalInfoStep";
import { AdditionalInfoStep } from "./AdditionalInfoStep";
import { REQUIRE_SIGNED_CARD_HELP, TRUSTED_JWKS_URLS_HELP, TRUSTED_JWKS_URLS_HINT } from "./CardTrustSections";

const card = { name: "Orders", description: "Handles orders." };

const REQUIRE_WITHOUT_URL = "Trusted JWKS URL required. Enter at least one URL.";
const lineMustBeTrusted = (line: number) => `Invalid format on line ${line}. Enter an https URL (http only on a loopback host such as localhost).`;

type StoredCardTrust = Pick<RemoteAgentResponse, "trustedCardJwksUrls" | "requireSignedCard">;

async function mountStep(agentCardLocation: string, defaultValues: Partial<AdditionalInfoFormData>, storedAgent?: StoredCardTrust) {
    let methods: UseFormReturn<AdditionalInfoFormData> | undefined;
    const { unmount } = renderWithProviders(
        <AdditionalInfoStep
            agentCardLocation={agentCardLocation}
            fetchedAgentCard={agentCardLocation === "none" ? null : card}
            defaultValues={{ taskAuthenticationType: "none", ...defaultValues }}
            initialRemoteAgent={storedAgent as RemoteAgentResponse | undefined}
            onFormReady={m => {
                methods = m;
            }}
        />
    );
    await waitFor(() => expect(methods).toBeDefined());
    return { methods: methods as UseFormReturn<AdditionalInfoFormData>, unmount };
}

async function renderStep(agentCardLocation: string, defaultValues: Partial<AdditionalInfoFormData>, storedAgent?: StoredCardTrust) {
    return (await mountStep(agentCardLocation, defaultValues, storedAgent)).methods;
}

const requireSignedCard = () => screen.getByRole("checkbox", { name: "Require Signed Card" });
const trustedUrls = () => screen.getByTestId("externalAgentInput-trustedCardJwksUrls");
const trustedUrlsRequiredMarker = () => within(screen.getByText("Trusted JWKS URLs")).queryByText("*");

async function validate(agentCardLocation: string, defaultValues: Partial<AdditionalInfoFormData>): Promise<boolean> {
    const methods = await renderStep(agentCardLocation, defaultValues);
    return methods.trigger();
}

describe("card trust on the invocation step", () => {
    test("the version and the signature requirement sit in one A2A Protocol section, and the URLs wait for the requirement", async () => {
        await renderStep("well_known", {});
        expect(screen.getByText("A2A Protocol")).toBeInTheDocument();
        expect(screen.queryByText("Agent Card Signature")).toBeNull();
        for (const label of ["A2A Version", "Require Signed Card"]) {
            expect(screen.getByText(label)).toBeInTheDocument();
        }
        expect(screen.getByText(/whether to check the signature of its agent card/)).toBeInTheDocument();
        expect(requireSignedCard()).not.toBeChecked();
        expect(screen.queryByText("Trusted JWKS URLs")).toBeNull();
        expect(screen.queryByTestId("externalAgentInput-trustedCardJwksUrls")).toBeNull();
    });

    test("the explanations are in the help next to each label, with an example under the URLs", async () => {
        await renderStep("well_known", { trustedCardJwksUrls: "https://keys.example.com/jwks.json", requireSignedCard: true });
        expect(screen.queryByText(REQUIRE_SIGNED_CARD_HELP)).toBeNull();
        await userEvent.hover(screen.getByRole("button", { name: "Require Signed Card help" }));
        expect(await screen.findByText(REQUIRE_SIGNED_CARD_HELP)).toBeInTheDocument();
        await userEvent.unhover(screen.getByRole("button", { name: "Require Signed Card help" }));
        await userEvent.hover(screen.getByRole("button", { name: "Trusted JWKS URLs help" }));
        expect(await screen.findByText(TRUSTED_JWKS_URLS_HELP)).toBeInTheDocument();
        expect(screen.getByText(TRUSTED_JWKS_URLS_HINT)).toBeInTheDocument();
        expect(trustedUrls()).not.toHaveAttribute("placeholder");
    });

    test("a card-less agent's A2A Protocol section offers only the version", async () => {
        await renderStep("none", { agentName: "Orders", agentDescription: "Handles orders." });
        expect(screen.getByText("A2A Protocol")).toBeInTheDocument();
        expect(screen.getByText("Select which version of the Agent-to-Agent (A2A) protocol the agent mesh uses with this agent. Without an agent card, Automatic uses A2A 0.3.")).toBeInTheDocument();
        expect(screen.queryByText("Trusted JWKS URLs")).toBeNull();
        expect(screen.queryByText("Require Signed Card")).toBeNull();
    });

    test.each([
        ["well_known", "auto", "Use the highest A2A version that both the agent card and Agent Mesh support."],
        ["well_known", "v1_0", "Always use A2A 1.0. The agent does not start if its card offers no A2A 1.0 interface."],
        ["none", "auto", "Use A2A 0.3. Without an agent card, the agent mesh cannot tell which versions the agent supports."],
        ["none", "v1_0", "Always use A2A 1.0. Select this when the agent supports A2A 1.0."],
        ["none", "v0_3", "Always use A2A 0.3, the older version."],
    ])("with card location %s the %s option says what it does", async (location, pin, description) => {
        await renderStep(location, location === "none" ? { agentName: "Orders", agentDescription: "Handles orders." } : {});
        await userEvent.click(screen.getByTestId("externalAgentSelect-protocolVersion"));
        expect(await screen.findByTestId(`externalAgentSelect-protocolVersion-option-${pin}`)).toHaveTextContent(description);
    });

    test("a new card agent starts on Automatic with no signature required, and validates as it is", async () => {
        const methods = await renderStep("well_known", {});
        expect(methods.getValues("protocolVersion")).toBe("auto");
        expect(methods.getValues("requireSignedCard")).toBe(false);
        expect(methods.getValues("trustedCardJwksUrls")).toBe("");
        expect(await methods.trigger()).toBe(true);
    });

    test("the A2A version select offers Automatic, A2A 1.0 and A2A 0.3", async () => {
        await renderStep("well_known", {});
        await userEvent.click(screen.getByTestId("externalAgentSelect-protocolVersion"));
        for (const [pin, label] of [
            ["auto", "Automatic"],
            ["v1_0", "A2A 1.0"],
            ["v0_3", "A2A 0.3"],
        ]) {
            expect(await screen.findByTestId(`externalAgentSelect-protocolVersion-option-${pin}`)).toHaveTextContent(label);
        }
        expect(screen.getAllByTestId(/^externalAgentSelect-protocolVersion-option-/)).toHaveLength(3);
    });

    test("requiring a signed card with no trusted key sets is refused on the URLs field", async () => {
        expect(await validate("well_known", { requireSignedCard: true, trustedCardJwksUrls: "" })).toBe(false);
        expect(await screen.findByText(REQUIRE_WITHOUT_URL)).toBeInTheDocument();
        expect(trustedUrls()).toHaveAttribute("aria-invalid", "true");
    });

    test("a key-set URL the platform would refuse is named by its line", async () => {
        expect(await validate("well_known", { requireSignedCard: true, trustedCardJwksUrls: "https://ok.example.com/jwks.json\nhttp://keys.example.com/jwks.json" })).toBe(false);
        expect(await screen.findByText(lineMustBeTrusted(2))).toBeInTheDocument();
        expect(screen.getByTestId("externalAgentInput-trustedCardJwksUrls")).toHaveAttribute("aria-invalid", "true");
    });

    test("valid trusted key sets and a required signature pass", async () => {
        expect(await validate("well_known", { trustedCardJwksUrls: "https://keys.example.com/jwks.json", requireSignedCard: true, protocolVersion: "v1_0" })).toBe(true);
        expect(screen.queryByText(REQUIRE_WITHOUT_URL)).toBeNull();
        expect(screen.queryByText(/Enter an https URL/)).toBeNull();
    });

    test("a card-less agent neither shows nor checks card trust", async () => {
        expect(await validate("none", { agentName: "Orders", agentDescription: "Handles orders.", requireSignedCard: true, trustedCardJwksUrls: "http://keys.example.com/jwks.json" })).toBe(true);
        expect(screen.queryByText(REQUIRE_WITHOUT_URL)).toBeNull();
        expect(screen.queryByText(/Enter an https URL/)).toBeNull();
        expect(screen.queryByTestId("externalAgentInput-trustedCardJwksUrls")).toBeNull();
        expect(screen.getByTestId("externalAgentSelect-protocolVersion")).toBeInTheDocument();
    });

    test("the checkbox shows a stored signature requirement as ticked", async () => {
        await renderStep("well_known", { trustedCardJwksUrls: "https://keys.example.com/jwks.json", requireSignedCard: true });
        expect(requireSignedCard()).toBeChecked();
        expect(trustedUrls()).toHaveValue("https://keys.example.com/jwks.json");
    });

    test("ticking the checkbox sets requireSignedCard and shows the URLs, with no error until they are checked", async () => {
        const methods = await renderStep("well_known", {});
        await userEvent.click(requireSignedCard());
        expect(methods.getValues("requireSignedCard")).toBe(true);
        expect(trustedUrls()).toBeInTheDocument();
        expect(screen.queryByText(REQUIRE_WITHOUT_URL)).toBeNull();
        expect(await methods.trigger()).toBe(false);
        expect(await screen.findByText(REQUIRE_WITHOUT_URL)).toBeInTheDocument();
    });

    test("unticking the checkbox drops the URLs typed for it, even an invalid one, and clears the error", async () => {
        const methods = await renderStep("well_known", {});
        await userEvent.click(requireSignedCard());
        await userEvent.type(trustedUrls(), "http://keys.example.com/jwks.json");
        await userEvent.tab();
        expect(await screen.findByText(lineMustBeTrusted(1))).toBeInTheDocument();
        await userEvent.click(requireSignedCard());
        expect(methods.getValues("requireSignedCard")).toBe(false);
        expect(methods.getValues("trustedCardJwksUrls")).toBe("");
        expect(screen.queryByTestId("externalAgentInput-trustedCardJwksUrls")).toBeNull();
        await waitFor(() => expect(methods.getFieldState("trustedCardJwksUrls").error).toBeUndefined());
        expect(await methods.trigger()).toBe(true);
    });

    test("ticking again after unticking shows an empty, required URLs field after the checkbox", async () => {
        await renderStep("well_known", {});
        await userEvent.click(requireSignedCard());
        await userEvent.type(trustedUrls(), "https://keys.example.com/jwks.json");
        await userEvent.click(requireSignedCard());
        await userEvent.click(requireSignedCard());
        expect(trustedUrls()).toHaveValue("");
        expect(requireSignedCard().compareDocumentPosition(trustedUrls()) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        expect(trustedUrlsRequiredMarker()).toBeInTheDocument();
    });

    test("trusted URLs stored without a requirement stay shown and are kept when the checkbox is ticked and unticked", async () => {
        const stored = "https://keys.example.com/jwks.json";
        const methods = await renderStep("well_known", { trustedCardJwksUrls: stored, requireSignedCard: false }, { trustedCardJwksUrls: [stored], requireSignedCard: false });
        expect(requireSignedCard()).not.toBeChecked();
        expect(trustedUrlsRequiredMarker()).toBeNull();
        expect(trustedUrls()).toHaveValue(stored);
        await userEvent.click(requireSignedCard());
        await userEvent.click(requireSignedCard());
        expect(methods.getValues("trustedCardJwksUrls")).toBe(stored);
        expect(trustedUrls()).toHaveValue(stored);
        expect(await methods.trigger()).toBe(true);
    });

    const typedUrl = "https://keys.example.com/jwks.json";
    test.each([
        ["stored with URLs and the requirement", { trustedCardJwksUrls: typedUrl, requireSignedCard: true }, { trustedCardJwksUrls: [typedUrl], requireSignedCard: true }],
        ["stored with no URLs and no requirement", { trustedCardJwksUrls: "", requireSignedCard: false }, { trustedCardJwksUrls: [], requireSignedCard: false }],
        ["that is new", {}, undefined],
    ] satisfies [string, Partial<AdditionalInfoFormData>, StoredCardTrust | undefined][])("unticking drops the URLs of an agent %s and hides the field", async (_, defaultValues, storedAgent) => {
        const methods = await renderStep("well_known", defaultValues, storedAgent);
        if (!methods.getValues("requireSignedCard")) {
            await userEvent.click(requireSignedCard());
            await userEvent.type(trustedUrls(), typedUrl);
        }
        await userEvent.click(requireSignedCard());
        expect(methods.getValues("trustedCardJwksUrls")).toBe("");
        expect(screen.queryByTestId("externalAgentInput-trustedCardJwksUrls")).toBeNull();
    });

    test("trusted URLs stored without a requirement are kept when the box is ticked, the step left and re-entered, then unticked", async () => {
        const stored = "https://keys.example.com/jwks.json";
        const storedAgent = { trustedCardJwksUrls: [stored], requireSignedCard: false };
        const first = await mountStep("well_known", { trustedCardJwksUrls: stored, requireSignedCard: false }, storedAgent);
        await userEvent.click(requireSignedCard());
        const leftWith = first.methods.getValues();
        first.unmount();
        const methods = await renderStep("well_known", leftWith, storedAgent);
        expect(requireSignedCard()).toBeChecked();
        await userEvent.click(requireSignedCard());
        expect(methods.getValues("trustedCardJwksUrls")).toBe(stored);
        expect(trustedUrls()).toHaveValue(stored);
    });

    test("URLs typed in a new agent are dropped on unticking, even after the step is left and re-entered", async () => {
        const first = await mountStep("well_known", {});
        await userEvent.click(requireSignedCard());
        await userEvent.type(trustedUrls(), "https://keys.example.com/jwks.json");
        const leftWith = first.methods.getValues();
        first.unmount();
        const methods = await renderStep("well_known", leftWith);
        await userEvent.click(requireSignedCard());
        expect(methods.getValues("trustedCardJwksUrls")).toBe("");
        expect(screen.queryByTestId("externalAgentInput-trustedCardJwksUrls")).toBeNull();
    });

    test("clearing the key sets of a card that must be signed says so on blur", async () => {
        await renderStep("well_known", { trustedCardJwksUrls: "https://keys.example.com/jwks.json", requireSignedCard: true });
        await userEvent.clear(trustedUrls());
        await userEvent.tab();
        expect(await screen.findByText(REQUIRE_WITHOUT_URL)).toBeInTheDocument();
    });

    test("an edited key-set list is checked on blur", async () => {
        await renderStep("well_known", {});
        await userEvent.click(requireSignedCard());
        await userEvent.type(trustedUrls(), "http://keys.example.com/jwks.json");
        await userEvent.tab();
        expect(await screen.findByText(lineMustBeTrusted(1))).toBeInTheDocument();
    });
});
