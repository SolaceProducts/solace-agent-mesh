// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { composeStory } from "@storybook/react";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test } from "vitest";

import meta, {
    BothCustomLogosCollapsed,
    BothCustomLogosExpanded,
    Collapsed,
    EmptyLogoDrawsDefault,
    EmptyLogoFallsThroughToSmallLogo,
    Expanded,
    SingleCustomLogoCollapsed,
    SingleCustomLogoExpanded,
    SmallOnlyExpanded,
} from "./CollapsibleNavigationSidebar.stories";

beforeEach(() => {
    sessionStorage.clear();
});

describe("CollapsibleNavigationSidebar — custom logos", () => {
    test("no custom logo: expanded slot shows Solace full icon", () => {
        const Story = composeStory(Expanded, meta);
        render(<Story />);

        expect(screen.queryByAltText("Logo")).not.toBeInTheDocument();
        expect(screen.getByAltText("Solace")).toBeInTheDocument();
    });

    test("no custom logo: collapsed slot shows Solace short icon", () => {
        const Story = composeStory(Collapsed, meta);
        render(<Story />);

        expect(screen.queryByAltText("Logo")).not.toBeInTheDocument();
        expect(screen.getByAltText("Solace")).toBeInTheDocument();
    });

    test("single configLogoUrl renders in the expanded slot", () => {
        const Story = composeStory(SingleCustomLogoExpanded, meta);
        render(<Story />);

        const logo = screen.getByAltText("Logo") as HTMLImageElement;
        expect(logo.src).toContain("Acme+Logo");
        expect(screen.queryByAltText("Solace")).not.toBeInTheDocument();
    });

    test("single configLogoUrl falls through into the collapsed slot", () => {
        const Story = composeStory(SingleCustomLogoCollapsed, meta);
        render(<Story />);

        const logo = screen.getByAltText("Logo") as HTMLImageElement;
        expect(logo.src).toContain("Acme");
        expect(screen.queryByAltText("Solace")).not.toBeInTheDocument();
    });

    test("single configSmallLogoUrl fans out into the expanded slot", () => {
        const Story = composeStory(SmallOnlyExpanded, meta);
        render(<Story />);

        const logo = screen.getByAltText("Logo") as HTMLImageElement;
        expect(logo.src).toContain("Small+Only");
        expect(screen.queryByAltText("Solace")).not.toBeInTheDocument();
    });

    test("distinct large logo renders in the expanded slot", () => {
        const Story = composeStory(BothCustomLogosExpanded, meta);
        render(<Story />);
        const logo = screen.getByAltText("Logo") as HTMLImageElement;
        expect(logo.src).toContain("Large+Logo");
        expect(logo.src).not.toContain("text=S");
    });

    test("distinct small logo renders in the collapsed slot", () => {
        const Story = composeStory(BothCustomLogosCollapsed, meta);
        render(<Story />);
        const logo = screen.getByAltText("Logo") as HTMLImageElement;
        expect(logo.src).toContain("text=S");
        expect(logo.src).not.toContain("Large+Logo");
    });

    test("empty configLogoUrl shows the Solace icon, not an empty slot", () => {
        const Story = composeStory(EmptyLogoDrawsDefault, meta);
        render(<Story />);

        expect(screen.queryByAltText("Logo")).not.toBeInTheDocument();
        expect(screen.getByAltText("Solace")).toBeInTheDocument();
    });

    test("empty configLogoUrl falls through to the configured small logo", () => {
        const Story = composeStory(EmptyLogoFallsThroughToSmallLogo, meta);
        render(<Story />);

        const logo = screen.getByAltText("Logo") as HTMLImageElement;
        expect(logo.src).toContain("Small+Only");
        expect(screen.queryByAltText("Solace")).not.toBeInTheDocument();
    });

    test("broken URL falls back to the Solace icon", () => {
        const Story = composeStory(SingleCustomLogoExpanded, meta);
        render(<Story />);

        fireEvent.error(screen.getByAltText("Logo"));
        expect(screen.queryByAltText("Logo")).not.toBeInTheDocument();
        expect(screen.getByAltText("Solace")).toBeInTheDocument();
    });

    test("error in collapsed slot does not poison the expanded slot on toggle", async () => {
        const user = userEvent.setup();
        const Story = composeStory(BothCustomLogosCollapsed, meta);
        render(<Story isCollapsed={undefined} defaultCollapsed={true} />);

        fireEvent.error(screen.getByAltText("Logo"));
        expect(screen.getByAltText("Solace")).toBeInTheDocument();

        const toggle = document.querySelector(".lucide-chevron-right")?.closest("button") as HTMLButtonElement;
        await user.click(toggle);

        const largeLogo = screen.getByAltText("Logo") as HTMLImageElement;
        expect(largeLogo.src).toContain("Large+Logo");
        expect(screen.queryByAltText("Solace")).not.toBeInTheDocument();
    });
});
