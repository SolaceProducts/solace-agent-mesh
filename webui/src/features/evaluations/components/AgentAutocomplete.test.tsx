// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import type { AgentOption } from "@/features/entrypoints/hooks";

import { AgentAutocomplete } from "./AgentAutocomplete";

describe("AgentAutocomplete", () => {
    test("shows the 'Not Deployed' badge only for undeployed agents in the dropdown", async () => {
        const options: AgentOption[] = [
            { name: "a", displayName: "Deployed Agent", deploymentStatus: "deployed" },
            { name: "b", displayName: "Undeployed Agent", deploymentStatus: "not_deployed" },
            { name: "c", displayName: "Discovered Agent" },
        ];
        render(<AgentAutocomplete options={options} value="" onChange={vi.fn()} />);

        await userEvent.setup().click(screen.getByRole("combobox"));

        expect(screen.getByText("Deployed Agent")).toBeInTheDocument();
        expect(screen.getByText("Undeployed Agent")).toBeInTheDocument();
        expect(screen.getByText("Discovered Agent")).toBeInTheDocument();
        expect(screen.getAllByText("Not Deployed")).toHaveLength(1);
    });
});
