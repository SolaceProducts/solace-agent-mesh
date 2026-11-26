// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { Metadata } from "./Metadata";

const AGENT_ID = "1b8f0c2a-4d3e-4f56-9a7b-0c1d2e3f4a5b";

const auditFields = {
    id: AGENT_ID,
    createdBy: "john.doe@example.org",
    createdAt: "2024-03-19T10:30:00Z",
    updatedBy: "jane.doe@example.org",
    updatedAt: "2024-03-19T14:45:00Z",
};

describe("Metadata", () => {
    test("renders the audit fields", () => {
        render(<Metadata metadata={auditFields} />);

        expect(screen.getByText("Created By:")).toBeInTheDocument();
        expect(screen.getByText("john.doe@example.org")).toBeInTheDocument();
        expect(screen.getByText("Last Updated By:")).toBeInTheDocument();
        expect(screen.getByText("jane.doe@example.org")).toBeInTheDocument();
        expect(screen.getByText("ID:")).toBeInTheDocument();
        expect(screen.getByText(AGENT_ID)).toBeInTheDocument();
    });

    test("capitalizes the system sentinel actor", () => {
        render(<Metadata metadata={{ ...auditFields, createdBy: "system", updatedBy: "system" }} />);

        expect(screen.getAllByText("System")).toHaveLength(2);
    });

    test("defaults absent fields: N/A for dates, dash for actors and ID", () => {
        render(<Metadata metadata={{}} />);

        expect(screen.getAllByText("N/A")).toHaveLength(2);
        expect(screen.getAllByText("-")).toHaveLength(3);
    });

    test("renders the scope row when a scope is provided", () => {
        render(<Metadata metadata={auditFields} scope={`agent:${AGENT_ID}:invoke`} />);

        expect(screen.getByText("Invoke Scope:")).toBeInTheDocument();
        expect(screen.getByText(`agent:${AGENT_ID}:invoke`)).toBeInTheDocument();
    });

    test("omits the scope row when no scope is provided", () => {
        render(<Metadata metadata={auditFields} />);

        expect(screen.queryByText("Invoke Scope:")).not.toBeInTheDocument();
    });
});
