// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import type { BuilderComponentProgress } from "@/types/builder";

import { BuilderProgressCard } from "./BuilderProgressCard";

const comp = (state: BuilderComponentProgress["state"]): BuilderComponentProgress => ({
    id: "agent:demo",
    type: "agent",
    name: "Demo",
    state,
    lastUpdated: 0,
});

describe("BuilderProgressCard header state", () => {
    test("building shows a spinner, not the success check", () => {
        const { container, getByText } = render(<BuilderProgressCard components={[comp("active")]} isBuilding />);
        getByText("Creating components...");
        expect(container.querySelector(".animate-spin")).not.toBeNull();
        expect(container.querySelector(".lucide-circle-check-big")).toBeNull();
    });

    test("failed build shows the failure header, not the success check", () => {
        const { container, getByText } = render(<BuilderProgressCard components={[comp("failed")]} isBuilding={false} />);
        getByText("Build failed");
        expect(container.querySelector(".lucide-circle-check-big")).toBeNull();
    });

    test("completed build shows the circle-check-big success icon", () => {
        const { container, getByText } = render(<BuilderProgressCard components={[comp("completed")]} isBuilding={false} />);
        getByText("Build complete");
        expect(container.querySelector(".lucide-circle-check-big")).not.toBeNull();
    });
});
