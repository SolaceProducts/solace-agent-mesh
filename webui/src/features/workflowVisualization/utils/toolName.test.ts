// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { nonEmptyString, splitToolName, toolNodeLabel } from "./toolName";

describe("splitToolName", () => {
    test("splits a toolset-prefixed name on the first separator", () => {
        expect(splitToolName("WeatherToolset__get_current_weather")).toEqual({
            toolset: "WeatherToolset",
            tool: "get_current_weather",
        });
    });

    test("splits only on the first separator, keeping later underscores in the tool", () => {
        expect(splitToolName("Toolset__do__thing")).toEqual({
            toolset: "Toolset",
            tool: "do__thing",
        });
    });

    test("returns only the tool when there is no separator", () => {
        expect(splitToolName("create_pptx")).toEqual({ tool: "create_pptx" });
    });

    test("falls back to the full name for a leading separator", () => {
        expect(splitToolName("__get_current_weather")).toEqual({ tool: "__get_current_weather" });
    });

    test("falls back to the full name for a trailing separator", () => {
        expect(splitToolName("WeatherToolset__")).toEqual({ tool: "WeatherToolset__" });
    });
});

describe("toolNodeLabel", () => {
    test("joins the connector and the tool", () => {
        expect(toolNodeLabel({ connectorName: "PetStoreAPI", toolName: "find_pets_by_status" }, "fetch_pets")).toBe("PetStoreAPI / find_pets_by_status");
    });

    test("drops the toolset prefix of a bundled tool name", () => {
        expect(toolNodeLabel({ toolName: "WeatherToolset__get_current_weather" }, "node")).toBe("get_current_weather");
    });

    test("shows the connector alone when no tool is named", () => {
        expect(toolNodeLabel({ connectorName: "SlackConn" }, "send")).toBe("SlackConn");
    });

    test("drops the connector prefix a deploy-resolved tool name carries", () => {
        expect(toolNodeLabel({ connectorName: "PetStoreAPI", toolName: "PetStoreAPI__find" }, "x")).toBe("PetStoreAPI / find");
    });

    test("treats an empty connector name as absent", () => {
        expect(toolNodeLabel({ connectorName: "", toolName: "send_msg" }, "send")).toBe("send_msg");
    });

    test("falls back to the given label when neither is set", () => {
        expect(toolNodeLabel({}, "fetch_pets")).toBe("fetch_pets");
    });
});

describe("nonEmptyString", () => {
    test("returns a non-empty string unchanged", () => {
        expect(nonEmptyString("create_pptx")).toBe("create_pptx");
    });

    test.each([
        ["an empty string", ""],
        ["a number", 123],
        ["a list", ["x"]],
        ["a map", { a: 1 }],
        ["null", null],
        ["undefined", undefined],
        ["a boolean", true],
    ])("treats %s as absent", (_label, value) => {
        expect(nonEmptyString(value)).toBeUndefined();
    });
});

describe("toolNodeLabel with a malformed tool name", () => {
    test.each([
        ["a number", 123],
        ["a list", ["x"]],
        ["a map", { a: 1 }],
    ])("shows the connector alone when the tool name is %s", (_label, toolName) => {
        expect(toolNodeLabel({ connectorName: "C", toolName: toolName as unknown as string }, "node")).toBe("C");
    });

    test.each([
        ["a number", 123],
        ["a list", ["x"]],
        ["a map", { a: 1 }],
    ])("falls back to the node label when the tool name is %s", (_label, toolName) => {
        expect(toolNodeLabel({ toolName: toolName as unknown as string }, "node")).toBe("node");
    });
});
