// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import type { EvalRunGroupResults, EvalRunResult } from "@/types/eval";

import { AuthRequiredBanner, summarizeFailures } from "./AuthRequiredBanner";

function trial(over: Partial<EvalRunResult>): EvalRunResult {
    return {
        id: "r1",
        runId: "run-1",
        exampleId: "ex-1",
        trialIndex: 0,
        scores: [],
        createdAt: "",
        ...over,
    };
}

describe("summarizeFailures", () => {
    test("counts nothing for missing results or clean trials", () => {
        expect(summarizeFailures(null)).toEqual({ authRequired: 0, other: 0 });
        expect(summarizeFailures(undefined)).toEqual({ authRequired: 0, other: 0 });
        expect(summarizeFailures({ groupId: "g1", resultsByModel: { "gpt-4": [trial({})] } })).toEqual({ authRequired: 0, other: 0 });
    });

    test("a failure without auth classification is an other failure", () => {
        const results: EvalRunGroupResults = {
            groupId: "g1",
            resultsByModel: { "gpt-4": [trial({}), trial({ exampleId: "ex-2", errorMessage: "other failure" })] },
        };
        expect(summarizeFailures(results)).toEqual({ authRequired: 0, other: 1 });
    });

    test("counts distinct examples per kind across models", () => {
        const results: EvalRunGroupResults = {
            groupId: "g1",
            resultsByModel: {
                "gpt-4": [trial({ failureKind: "auth_required" }), trial({ exampleId: "ex-2", failureKind: "auth_required" })],
                claude: [trial({ failureKind: "auth_required" }), trial({ exampleId: "ex-3" }), trial({ exampleId: "ex-4", errorMessage: "timeout", failureKind: "timeout" })],
            },
        };
        expect(summarizeFailures(results)).toEqual({ authRequired: 2, other: 1 });
    });
});

describe("AuthRequiredBanner", () => {
    test("renders nothing at count 0", () => {
        render(<AuthRequiredBanner count={0} onConnectTools={vi.fn()} />);
        expect(screen.queryByTestId("authRequiredBanner")).not.toBeInTheDocument();
    });

    test("renders the count and routes Connect Tools clicks", async () => {
        const user = userEvent.setup();
        const onConnectTools = vi.fn();
        render(<AuthRequiredBanner count={3} onConnectTools={onConnectTools} />);

        expect(screen.getByText("3 examples blocked on tool authorization")).toBeInTheDocument();
        await user.click(screen.getByRole("button", { name: "Connect Tools" }));
        expect(onConnectTools).toHaveBeenCalledTimes(1);
    });

    test("names the workflow on a workflow experiment", () => {
        render(<AuthRequiredBanner count={2} targetIsWorkflow />);
        expect(screen.getByText(/The workflow needed a tool login/)).toBeInTheDocument();
    });

    test("singular copy and no button without a handler", () => {
        render(<AuthRequiredBanner count={1} />);
        expect(screen.getByText("1 example blocked on tool authorization")).toBeInTheDocument();
        expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });
});
