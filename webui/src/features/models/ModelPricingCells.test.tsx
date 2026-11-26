// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { render, screen, within } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import type { ModelPricing } from "@/api/models/types";

import { Table, TableBody, TableRow } from "@/ui";

import { ModelPricingCells } from "./ModelPricingCells";

expect.extend(matchers);

function renderCells(pricing: ModelPricing | null) {
    return render(
        <Table>
            <TableBody>
                <TableRow>
                    <ModelPricingCells pricing={pricing} />
                </TableRow>
            </TableBody>
        </Table>
    );
}

function cells() {
    return within(screen.getByRole("row")).getAllByRole("cell");
}

describe("ModelPricingCells", () => {
    test("a model nobody has priced shows a dash in both columns", () => {
        renderCells(null);

        const rendered = cells();
        expect(rendered).toHaveLength(2);
        for (const cell of rendered) {
            expect(cell).toHaveTextContent("—");
        }
    });

    test("stated rates render as currency, padded to the cent, with the cache rates in the input hint", () => {
        renderCells({ input: "3", output: "15", cacheRead: "0.3", cacheWrite: "3.75" });

        const [input, output] = cells();
        expect(input).toHaveTextContent("$3.00");
        expect(input).toHaveTextContent("(Cache read: $0.30 Cache write: $3.75)");
        expect(output).toHaveTextContent("$15.00");
    });

    test("unstated cache rates are named in the input hint as defaulting to the input rate", () => {
        renderCells({ input: "3", output: "15", cacheRead: null, cacheWrite: null });

        const [input] = cells();
        expect(input).toHaveTextContent("(Cache read and cache write default to the input price.)");
    });

    test("a free model shows zero rather than a dash", () => {
        renderCells({ input: "0", output: "0", cacheRead: null, cacheWrite: null });

        const [input, output] = cells();
        expect(input).toHaveTextContent("$0.00");
        expect(output).toHaveTextContent("$0.00");
        expect(input).not.toHaveTextContent("—");
    });
});
