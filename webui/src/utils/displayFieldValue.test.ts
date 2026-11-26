// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { formatDisplayFieldValue } from "./displayFieldValue";

describe("formatDisplayFieldValue", () => {
    test("masks password values", () => {
        expect(formatDisplayFieldValue("hunter2", "password")).toBe("••••••••");
    });

    test("formats numbers and dashes non-numbers", () => {
        expect(formatDisplayFieldValue(42, "number")).toBe("42");
        expect(formatDisplayFieldValue(0, "number")).toBe("0");
        expect(formatDisplayFieldValue(undefined, "number")).toBe("—");
        expect(formatDisplayFieldValue(NaN, "number")).toBe("—");
    });

    test("formats booleans", () => {
        expect(formatDisplayFieldValue(true, "boolean")).toBe("True");
        expect(formatDisplayFieldValue(false, "boolean")).toBe("False");
    });

    test("select resolves the option label, falling back to the raw value", () => {
        const options = [{ value: "a", label: "Apple" }];
        expect(formatDisplayFieldValue("a", "select", options)).toBe("Apple");
        expect(formatDisplayFieldValue("b", "select", options)).toBe("b");
        expect(formatDisplayFieldValue("b", "select")).toBe("b");
        expect(formatDisplayFieldValue(undefined, "select", options)).toBe("—");
    });

    test("formats key/value pairs from arrays and objects, dashing when empty", () => {
        expect(formatDisplayFieldValue([{ key: "a", value: "1" }], "key_value")).toBe("a: 1");
        expect(formatDisplayFieldValue({ a: "1", b: "2" }, "key_value")).toBe("a: 1, b: 2");
        expect(formatDisplayFieldValue([], "key_value")).toBe("—");
        expect(formatDisplayFieldValue(null, "key_value")).toBe("—");
    });

    test("default case stringifies primitives, dashing null/undefined/objects", () => {
        expect(formatDisplayFieldValue("hello", "text")).toBe("hello");
        expect(formatDisplayFieldValue(7, "text")).toBe("7");
        expect(formatDisplayFieldValue(undefined, "text")).toBe("—");
        expect(formatDisplayFieldValue(null, "text")).toBe("—");
        expect(formatDisplayFieldValue({ a: 1 }, "text")).toBe("—");
    });
});
