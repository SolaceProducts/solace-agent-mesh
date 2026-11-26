// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { composeStory } from "@storybook/react";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import meta, { AgentIconWinsOverDeploymentLogo, Default, EmptyDeploymentLogoDrawsDefault, WithDeploymentLogo, WithSmallDeploymentLogoOnly } from "./ChatWelcomeScreen.stories";

describe("ChatWelcomeScreen — deployment logo customization", () => {
    test("no config logo and no welcome_icon: shows the Solace short icon", () => {
        const Story = composeStory(Default, meta);
        render(<Story />);

        expect(screen.queryByAltText("Logo")).not.toBeInTheDocument();
        expect(screen.getByAltText("Solace")).toBeInTheDocument();
    });

    test("configLogoUrl renders above the welcome message", () => {
        const Story = composeStory(WithDeploymentLogo, meta);
        render(<Story />);

        const logo = screen.getByAltText("Logo") as HTMLImageElement;
        expect(logo.src).toContain("Deployment+Logo");
        expect(screen.queryByAltText("Solace")).not.toBeInTheDocument();
    });

    test("configSmallLogoUrl is used when the full logo is not configured", () => {
        const Story = composeStory(WithSmallDeploymentLogoOnly, meta);
        render(<Story />);

        const logo = screen.getByAltText("Logo") as HTMLImageElement;
        expect(logo.src).toContain("Small+Only");
        expect(screen.queryByAltText("Solace")).not.toBeInTheDocument();
    });

    test("empty logo values show the Solace icon, not an empty slot", () => {
        const Story = composeStory(EmptyDeploymentLogoDrawsDefault, meta);
        render(<Story />);

        expect(screen.queryByAltText("Logo")).not.toBeInTheDocument();
        expect(screen.getByAltText("Solace")).toBeInTheDocument();
    });

    test("agent welcome_icon wins over the deployment logo", () => {
        const Story = composeStory(AgentIconWinsOverDeploymentLogo, meta);
        render(<Story />);

        expect(document.querySelector('img[src="https://placehold.co/120x120/png"]')).not.toBeNull();
        expect(screen.queryByAltText("Logo")).not.toBeInTheDocument();
        expect(screen.queryByAltText("Solace")).not.toBeInTheDocument();
    });

    test("broken deployment logo URL falls back to the Solace icon", () => {
        const Story = composeStory(WithDeploymentLogo, meta);
        render(<Story />);

        fireEvent.error(screen.getByAltText("Logo"));
        expect(screen.queryByAltText("Logo")).not.toBeInTheDocument();
        expect(screen.getByAltText("Solace")).toBeInTheDocument();
    });
});
