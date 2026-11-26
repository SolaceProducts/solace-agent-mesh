// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { ActivityPanelHeader } from "./ActivityPanelHeader";

describe("ActivityPanelHeader", () => {
    test("renders the title and a Back button", () => {
        render(<ActivityPanelHeader title="Test 2 Activity" onBack={vi.fn()} />);
        expect(screen.getByText("Test 2 Activity")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: /back/i })).toBeInTheDocument();
    });

    test("clicking Back invokes onBack", () => {
        const onBack = vi.fn();
        render(<ActivityPanelHeader title="Activity" onBack={onBack} backTooltip="Back to components" />);
        fireEvent.click(screen.getByRole("button", { name: /back to components/i }));
        expect(onBack).toHaveBeenCalledOnce();
    });
});
