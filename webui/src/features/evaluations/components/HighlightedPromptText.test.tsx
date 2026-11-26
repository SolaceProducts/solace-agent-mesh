// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { HighlightedPromptText } from "./HighlightedPromptText";

describe("HighlightedPromptText", () => {
    test("highlights a workflow node placeholder like the other variables", () => {
        render(<HighlightedPromptText text="Grade {{Node Output: classify}} against {{Response}}." />);

        expect(screen.getByText("{{Node Output: classify}}").className).toMatch(/info-wMain/);
        expect(screen.getByText("{{Response}}").className).toMatch(/info-wMain/);
        expect(screen.getByText("Grade").className).not.toMatch(/info-wMain/);
    });
});
