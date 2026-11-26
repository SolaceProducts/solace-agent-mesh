// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { StatusBadge } from "./StatusBadge";

describe("StatusBadge", () => {
    test("quiet run reads 'Nothing to report' in the neutral style", () => {
        render(<StatusBadge status="quiet" />);
        expect(screen.getByText("Nothing to report").className).toContain("secondary");
    });

    test("an unknown status shows its raw value", () => {
        render(<StatusBadge status="mystery" />);
        expect(screen.getByText("mystery")).toBeInTheDocument();
    });
});
