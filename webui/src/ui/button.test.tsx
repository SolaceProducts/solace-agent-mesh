// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { Button } from "./button";

describe("Button", () => {
    test("a tooltip names an icon-only button", () => {
        render(<Button tooltip="Remove">x</Button>);
        expect(screen.getByRole("button", { name: "Remove" })).toBeInTheDocument();
    });

    test("an explicit aria-label wins over the tooltip", () => {
        render(
            <Button tooltip="Remove" aria-label="Remove Raj Patel">
                x
            </Button>
        );
        expect(screen.getByRole("button", { name: "Remove Raj Patel" })).toBeInTheDocument();
    });
});
