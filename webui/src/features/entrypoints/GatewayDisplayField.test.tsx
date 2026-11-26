// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { GatewayDisplayField } from "./GatewayDisplayField";

describe("GatewayDisplayField", () => {
    test.each([
        ["an unset value", undefined, "text"],
        ["a null value", null, "text"],
        ["an empty string, as an optional field saved empty is stored", "", "text"],
        ["an empty string in a select", "", "select"],
    ])("shows the dash for %s", (_name, value, fieldType) => {
        render(<GatewayDisplayField label="Endpoint URL" value={value} fieldType={fieldType} />);

        expect(screen.getByText("—")).toBeInTheDocument();
    });

    test.each([
        ["a string", "https://sam.example.com/gw/hr/events", "text", "https://sam.example.com/gw/hr/events"],
        ["zero", 0, "number", "0"],
        ["a true boolean", true, "boolean", "True"],
        ["a false boolean", false, "boolean", "False"],
    ])("shows %s as itself", (_name, value, fieldType, expected) => {
        render(<GatewayDisplayField label="Value" value={value} fieldType={fieldType} />);

        expect(screen.getByText(expected)).toBeInTheDocument();
        expect(screen.queryByText("—")).not.toBeInTheDocument();
    });

    test("lists every entry of a list value", () => {
        render(<GatewayDisplayField label="Allowed email domains" value={["example.com", "example.org"]} fieldType="text" />);

        expect(screen.getByText("example.com, example.org")).toBeInTheDocument();
    });

    test.each([
        ["an empty list", []],
        ["a list of one empty string", [""]],
        ["a list of objects", [{ a: 1 }]],
    ])("shows the dash for %s", (_name, value) => {
        render(<GatewayDisplayField label="Allowed email domains" value={value} fieldType="text" />);

        expect(screen.getByText("—")).toBeInTheDocument();
    });

    test("leaves the empty entries out of a list", () => {
        render(<GatewayDisplayField label="Allowed email domains" value={["example.com", "", 7, null]} fieldType="text" />);

        expect(screen.getByText("example.com, 7")).toBeInTheDocument();
    });

    test("masks a secret whatever it holds", () => {
        render(<GatewayDisplayField label="Service account key" value="{}" fieldType="password" />);

        expect(screen.getByText("••••••••")).toBeInTheDocument();
    });
});
