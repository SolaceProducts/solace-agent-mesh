// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { CollapsePanelIcon } from "./CollapsePanelIcon";

describe("CollapsePanelIcon", () => {
    test("renders a decorative svg with the supplied class", () => {
        const { container } = render(<CollapsePanelIcon className="size-4" />);
        const svg = container.querySelector("svg");
        expect(svg).not.toBeNull();
        expect(svg).toHaveClass("size-4");
        expect(svg).toHaveAttribute("aria-hidden", "true");
        expect(svg?.querySelector("path")).not.toBeNull();
    });
});
