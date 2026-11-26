// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, test } from "vitest";

import { disableWailsRuntime, enableWailsRuntime } from "@/testing/helpers/wailsRuntime";

import { NewTabLink } from "./NewTabLink";

function renderLink() {
    return render(
        <MemoryRouter>
            <NewTabLink to="/agent-mesh/123" label="My Agent" />
        </MemoryRouter>
    );
}

describe("NewTabLink", () => {
    afterEach(() => disableWailsRuntime());

    test("opens in a new tab in the browser", () => {
        disableWailsRuntime();
        renderLink();
        const link = screen.getByRole("link", { name: /my agent/i });
        expect(link).toHaveAttribute("href", "/agent-mesh/123");
        expect(link).toHaveAttribute("target", "_blank");
        expect(link).toHaveAttribute("rel", "noopener noreferrer");
    });

    test("navigates in-app (no new tab) in the desktop shell", () => {
        enableWailsRuntime();
        renderLink();
        const link = screen.getByRole("link", { name: /my agent/i });
        expect(link).toHaveAttribute("href", "/agent-mesh/123");
        expect(link).not.toHaveAttribute("target");
        expect(link).not.toHaveAttribute("rel");
    });

    test("renders a single anchor with no nested button", () => {
        disableWailsRuntime();
        renderLink();
        expect(screen.queryByRole("button")).toBeNull();
        expect(screen.getAllByRole("link")).toHaveLength(1);
    });

    test("an external URL is a plain anchor that opens a new tab", () => {
        render(
            <MemoryRouter>
                <NewTabLink to="https://docs.solace.com/x" label="docs" />
            </MemoryRouter>
        );
        const link = screen.getByRole("link", { name: /docs/i });
        expect(link).toHaveAttribute("href", "https://docs.solace.com/x");
        expect(link).toHaveAttribute("target", "_blank");
        expect(link).toHaveAttribute("rel", "noopener noreferrer");
    });

    test("an external URL keeps target=_blank in the desktop shell", () => {
        enableWailsRuntime();
        render(
            <MemoryRouter>
                <NewTabLink to="https://docs.solace.com/x" label="docs" />
            </MemoryRouter>
        );
        const link = screen.getByRole("link", { name: /docs/i });
        expect(link).toHaveAttribute("target", "_blank");
    });
});
