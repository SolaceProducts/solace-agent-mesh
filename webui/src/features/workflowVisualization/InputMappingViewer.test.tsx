// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import InputMappingViewer from "@/features/workflowVisualization/InputMappingViewer";

describe("InputMappingViewer", () => {
    test("renders empty-state message when mapping is null (guards Object.entries)", () => {
        render(<InputMappingViewer mapping={null as unknown as Record<string, unknown>} />);
        expect(screen.getByText(/No input mapping defined/i)).toBeInTheDocument();
    });

    test("renders empty-state message when mapping is undefined", () => {
        render(<InputMappingViewer mapping={undefined as unknown as Record<string, unknown>} />);
        expect(screen.getByText(/No input mapping defined/i)).toBeInTheDocument();
    });

    test("renders empty-state message when mapping is {}", () => {
        render(<InputMappingViewer mapping={{}} />);
        expect(screen.getByText(/No input mapping defined/i)).toBeInTheDocument();
    });

    test("renders mapping entries when values are primitives", () => {
        render(<InputMappingViewer mapping={{ alpha: "one", beta: 42, gamma: true }} />);
        expect(screen.getByText("alpha")).toBeInTheDocument();
        expect(screen.getByText("beta")).toBeInTheDocument();
        expect(screen.getByText("gamma")).toBeInTheDocument();
    });

    test("handles nested object whose inner value is null (guards Object.entries recursion)", () => {
        const mapping: Record<string, unknown> = {
            wrapper: { innerNull: null, innerNumber: 7 },
        };
        render(<InputMappingViewer mapping={mapping} />);
        expect(screen.getByText("wrapper")).toBeInTheDocument();
    });
});
