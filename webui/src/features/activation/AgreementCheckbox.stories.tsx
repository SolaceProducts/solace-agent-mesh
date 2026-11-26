// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import type { ClickwrapTerms } from "@/api/activation";

import { AgreementCheckbox } from "@/features/activation/AgreementCheckbox";
import { EULA_FALLBACK } from "@/features/activation/agreementText";

const EULA = { id: "201", name: "Solace Agent Mesh Desktop EULA", version: "1.1", url: "https://legal.example.test/eula" };
const PRIVACY = { id: "203", name: "Privacy Notice", version: "2.0", url: "https://legal.example.test/privacy" };
const eulaTerms: ClickwrapTerms = { agreed: false, checkboxText: "I agree to {#agreement_list#} as per laws.", documents: [EULA, PRIVACY] };

function Controlled({ terms, disabled = false, onOpen }: Readonly<{ terms: ClickwrapTerms | undefined; disabled?: boolean; onOpen: (id: string) => void }>) {
    const [checked, setChecked] = useState(false);
    return <AgreementCheckbox id="story-eula" terms={terms} fallback={EULA_FALLBACK} checked={checked} disabled={disabled} onCheckedChange={setChecked} onOpen={onOpen} />;
}

const meta = {
    title: "Activation/AgreementCheckbox",
    component: Controlled,
    parameters: {
        docs: { description: { component: "One license agreement box on the welcome: SpotDraft's checkbox wording with its documents as links, or our own wording when SpotDraft's has no agreement list." } },
    },
    args: { terms: eulaTerms, onOpen: fn() },
} satisfies Meta<typeof Controlled>;

export default meta;
type Story = StoryObj<typeof meta>;

export const SpotDraftWording: Story = {
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const box = canvas.getByRole("checkbox", { name: "I agree to Solace Agent Mesh Desktop EULA and Privacy Notice as per laws." });
        expect(box).not.toBeChecked();
        const privacy = canvas.getByRole("link", { name: "Privacy Notice" });
        privacy.addEventListener("click", event => event.preventDefault(), { once: true });
        await userEvent.click(privacy);
        expect(args.onOpen).toHaveBeenCalledWith(PRIVACY.id);
        await userEvent.click(box);
        expect(box).toBeChecked();
    },
};

export const OwnWording: Story = {
    args: { terms: { ...eulaTerms, checkboxText: "" } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByRole("checkbox", { name: "I agree to the Solace Agent Mesh End User License Agreement" })).toBeInTheDocument();
        expect(canvas.getByRole("link", { name: "Solace Agent Mesh End User License Agreement" })).toHaveAttribute("href", EULA.url);
    },
};

export const Loading: Story = {
    args: { terms: undefined },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByRole("checkbox")).toBeDisabled();
        expect(canvas.getByLabelText("Loading the agreement")).toBeInTheDocument();
    },
};
