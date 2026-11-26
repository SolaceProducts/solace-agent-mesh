// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { TimelineRow } from "./TimelineRow";

describe("TimelineRow", () => {
    function railCount(isFirst: boolean, isLast: boolean) {
        const { container } = render(
            <TimelineRow dot={<div data-testid="dot" />} isFirst={isFirst} isLast={isLast} railClassName="rail-probe">
                <span>row body</span>
            </TimelineRow>
        );
        return container.querySelectorAll(".rail-probe").length;
    }

    test("a middle row draws both connector segments", () => {
        expect(railCount(false, false)).toBe(2);
    });

    test("the first row omits the upward segment (draws only downward)", () => {
        expect(railCount(true, false)).toBe(1);
    });

    test("the last row omits the downward segment (draws only upward)", () => {
        expect(railCount(false, true)).toBe(1);
    });

    test("a lone row (first and last) draws no rail", () => {
        expect(railCount(true, true)).toBe(0);
    });

    test("renders the dot and content", () => {
        render(
            <TimelineRow dot={<div data-testid="dot" />} isFirst isLast railClassName="rail-probe">
                <span>hello row</span>
            </TimelineRow>
        );
        expect(screen.getByTestId("dot")).toBeInTheDocument();
        expect(screen.getByText("hello row")).toBeInTheDocument();
    });
});
