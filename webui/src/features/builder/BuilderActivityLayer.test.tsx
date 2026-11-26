// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { BuilderActivityLayer } from "./BuilderActivityLayer";

describe("BuilderActivityLayer", () => {
    test("renders the Activity title and its children", () => {
        render(
            <BuilderActivityLayer onBack={vi.fn()}>
                <div>flow content</div>
            </BuilderActivityLayer>
        );
        expect(screen.getByText("Activity")).toBeInTheDocument();
        expect(screen.getByText("flow content")).toBeInTheDocument();
    });

    test("clicking Back invokes onBack", () => {
        const onBack = vi.fn();
        render(
            <BuilderActivityLayer onBack={onBack}>
                <div />
            </BuilderActivityLayer>
        );
        fireEvent.click(screen.getByRole("button", { name: /back to chat/i }));
        expect(onBack).toHaveBeenCalledOnce();
    });
});
