// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { ModelProviderIcon } from "./ModelProviderIcon";

describe("ModelProviderIcon", () => {
    test("renders provider icon for known provider", () => {
        const { container } = render(<ModelProviderIcon provider="openai" />);
        const img = container.querySelector("img");
        expect(img).toBeInTheDocument();
        expect(img).toHaveAttribute("alt", "openai");
    });

    test("renders fallback initials for unknown provider", () => {
        const { container } = render(<ModelProviderIcon provider="unknown_provider" />);
        const span = container.querySelector("span");
        expect(span).toHaveTextContent("U");
    });

    test("renders a glyph (not initials) for the custom provider", () => {
        const { container } = render(<ModelProviderIcon provider="custom" />);
        expect(container.querySelector("svg")).toBeInTheDocument();
        expect(container.querySelector("img")).not.toBeInTheDocument();
        expect(container.querySelector("span")).not.toBeInTheDocument();
    });
});
