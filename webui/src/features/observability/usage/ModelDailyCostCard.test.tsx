// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { usageModelRows } from "@/testing/data/usage";

import { ModelDailyCostCard } from "./ModelDailyCostCard";

describe("ModelDailyCostCard", () => {
    test("says the chart can't load when the daily total fails, rather than loading for ever", () => {
        render(<ModelDailyCostCard rows={usageModelRows} isStale={false} isLoading={false} error={null} dailyError={new Error("Service unavailable")} today="2026-10-19" />);

        expect(screen.getByRole("region", { name: "Daily Cost by Model" })).toHaveTextContent(/Couldn't load daily cost by model/);
    });
});
