// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, test } from "vitest";

import { disableWailsRuntime, enableWailsRuntime } from "@/testing/helpers/wailsRuntime";

import { NumberedStep } from "./NumberedStep";

function renderStep(href: string) {
    return render(
        <MemoryRouter>
            <NumberedStep number={1} title="Step" description="desc" button={{ label: "Go", href }} />
        </MemoryRouter>
    );
}

describe("NumberedStep button link", () => {
    afterEach(() => disableWailsRuntime());

    test("external href renders a plain anchor that opens a new tab", () => {
        renderStep("https://docs.solace.com/x");
        const link = screen.getByRole("link", { name: /go/i });
        expect(link).toHaveAttribute("href", "https://docs.solace.com/x");
        expect(link).toHaveAttribute("target", "_blank");
        expect(link).toHaveAttribute("rel", "noopener noreferrer");
    });

    test("internal href opens a new tab in the browser", () => {
        disableWailsRuntime();
        renderStep("/toolsets");
        const link = screen.getByRole("link", { name: /go/i });
        expect(link).toHaveAttribute("href", "/toolsets");
        expect(link).toHaveAttribute("target", "_blank");
    });

    test("internal href navigates in-app in the desktop shell (no new tab)", () => {
        enableWailsRuntime();
        renderStep("/toolsets");
        const link = screen.getByRole("link", { name: /go/i });
        expect(link).toHaveAttribute("href", "/toolsets");
        expect(link).not.toHaveAttribute("target");
    });
});
