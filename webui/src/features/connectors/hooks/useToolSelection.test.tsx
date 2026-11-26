// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { FormProvider, useForm } from "react-hook-form";
import { describe, expect, test } from "vitest";

import type { MCPTool, Tool } from "@/types";

import { useToolSelection } from "./useToolSelection";

const createTool = (id: string): Tool => ({
    operationId: id,
    name: id,
    description: `Description for ${id}`,
    method: "GET",
    endpoint: `/api/${id}`,
});

const createMCPTool = (name: string): MCPTool => ({
    name,
    description: `MCP tool ${name}`,
    inputSchema: {},
});

const createWrapper = (defaultValues: any = {}) => {
    return ({ children }: { children: ReactNode }) => {
        const methods = useForm({
            defaultValues: {
                tools: undefined,
                mcpTools: undefined,
                "values.allow_list": "",
                ...defaultValues,
            },
        });
        return <FormProvider {...methods}>{children}</FormProvider>;
    };
};

describe("useToolSelection", () => {
    test("parses and validates allow_list with whitespace trimming", () => {
        const { result } = renderHook(() => useToolSelection({ fieldPath: "values.allow_list" }), {
            wrapper: createWrapper({
                tools: [createTool("tool1"), createTool("tool2")],
                "values.allow_list": " tool1 , tool2 ",
            }),
        });

        expect(result.current.selectedToolIds.size).toBe(2);
        expect(result.current.selectedToolIds.has("tool1")).toBe(true);
        expect(result.current.selectedToolIds.has("tool2")).toBe(true);
    });

    test("defaults to all tools when all saved selections invalid", () => {
        const { result } = renderHook(() => useToolSelection({ fieldPath: "values.allow_list" }), {
            wrapper: createWrapper({
                tools: [createTool("tool1"), createTool("tool2")],
                "values.allow_list": "invalid1,invalid2",
            }),
        });

        expect(result.current.selectedToolIds.size).toBe(2);
    });

    test("initializes with all tools selected by default", () => {
        const { result } = renderHook(() => useToolSelection({ fieldPath: "values.allow_list" }), {
            wrapper: createWrapper({ tools: [createTool("tool1"), createTool("tool2")] }),
        });

        expect(result.current.selectedToolIds.size).toBe(2);
    });

    test("restores selections from allow_list", () => {
        const { result } = renderHook(() => useToolSelection({ fieldPath: "values.allow_list" }), {
            wrapper: createWrapper({
                tools: [createTool("tool1"), createTool("tool2"), createTool("tool3")],
                "values.allow_list": "tool1,tool3",
            }),
        });

        expect(result.current.selectedToolIds.has("tool1")).toBe(true);
        expect(result.current.selectedToolIds.has("tool2")).toBe(false);
        expect(result.current.selectedToolIds.has("tool3")).toBe(true);
    });

    test("supports MCP tools", () => {
        const { result } = renderHook(() => useToolSelection({ fieldPath: "values.allow_list" }), {
            wrapper: createWrapper({ mcpTools: [createMCPTool("mcpTool1")] }),
        });

        expect(result.current.tools?.[0].method).toBe("MCP");
    });

    test("toggleTool changes selection", () => {
        const { result } = renderHook(() => useToolSelection({ fieldPath: "values.allow_list" }), {
            wrapper: createWrapper({ tools: [createTool("tool1"), createTool("tool2")] }),
        });

        act(() => {
            result.current.toggleTool("tool2");
        });

        expect(result.current.selectedToolIds.has("tool2")).toBe(false);
    });

    test("filters tools by search", () => {
        const { result } = renderHook(() => useToolSelection({ fieldPath: "values.allow_list" }), {
            wrapper: createWrapper({
                tools: [createTool("getUserById"), createTool("createUser"), createTool("deletePost")],
            }),
        });

        act(() => {
            result.current.setFilter("user");
        });

        expect(result.current.filteredTools.length).toBe(2);
    });

    test("selectAll adds filtered tools to selection", () => {
        const { result } = renderHook(() => useToolSelection({ fieldPath: "values.allow_list" }), {
            wrapper: createWrapper({ tools: [createTool("tool1"), createTool("tool2")] }),
        });

        act(() => {
            result.current.deselectAll();
        });

        expect(result.current.selectedToolIds.size).toBe(0);

        act(() => {
            result.current.selectAll();
        });

        expect(result.current.selectedToolIds.size).toBe(2);
    });

    test("deselectAll removes filtered tools from selection", () => {
        const { result } = renderHook(() => useToolSelection({ fieldPath: "values.allow_list" }), {
            wrapper: createWrapper({
                tools: [createTool("tool1"), createTool("tool2"), createTool("other")],
            }),
        });

        act(() => {
            result.current.setFilter("tool");
        });

        act(() => {
            result.current.deselectAll();
        });

        expect(result.current.selectedToolIds.has("other")).toBe(true);
        expect(result.current.selectedToolIds.has("tool1")).toBe(false);
    });

    test("updates form field when selections change", () => {
        let formMethods: any;

        const Wrapper = ({ children }: { children: ReactNode }) => {
            formMethods = useForm({
                defaultValues: {
                    tools: [createTool("tool1"), createTool("tool2")],
                    "values.allow_list": "",
                },
            });
            return <FormProvider {...formMethods}>{children}</FormProvider>;
        };

        const { result } = renderHook(() => useToolSelection({ fieldPath: "values.allow_list" }), {
            wrapper: Wrapper,
        });

        act(() => {
            result.current.toggleTool("tool2");
        });

        expect(formMethods.getValues("values.allow_list")).toBe("tool1");
    });
});
