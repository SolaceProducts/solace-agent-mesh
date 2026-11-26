// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { extractRagDataFromTasks, parseTaskMetadata } from "@/utils/taskUtils";

describe("parseTaskMetadata", () => {
    test("returns null for falsy values", () => {
        expect(parseTaskMetadata(null)).toBeNull();
        expect(parseTaskMetadata(undefined)).toBeNull();
        expect(parseTaskMetadata("")).toBeNull();
        expect(parseTaskMetadata(0)).toBeNull();
    });

    test("returns the object directly when given an object", () => {
        const obj = { ragData: [], feedback: { type: "up" } };
        expect(parseTaskMetadata(obj)).toBe(obj);
    });

    test("parses valid JSON strings", () => {
        const json = '{"ragData": [{"query": "test"}], "agentName": "myAgent"}';
        const result = parseTaskMetadata(json);
        expect(result).toEqual({ ragData: [{ query: "test" }], agentName: "myAgent" });
    });

    test("returns null for invalid JSON strings", () => {
        expect(parseTaskMetadata("{invalid json}")).toBeNull();
        expect(parseTaskMetadata("not json at all")).toBeNull();
    });

    test("returns null for non-string, non-object types", () => {
        expect(parseTaskMetadata(42)).toBeNull();
        expect(parseTaskMetadata(true)).toBeNull();
    });
});

describe("extractRagDataFromTasks", () => {
    test("returns empty array for empty tasks", () => {
        expect(extractRagDataFromTasks([])).toEqual([]);
    });

    test("extracts RAG data from tasks with object metadata", () => {
        const tasks = [
            {
                taskId: "task-1",
                taskMetadata: {
                    ragData: [
                        { query: "search1", results: ["r1"] },
                        { query: "search2", results: ["r2"] },
                    ],
                },
            },
        ];
        const result = extractRagDataFromTasks(tasks);
        expect(result).toHaveLength(2);
        expect(result[0]).toEqual({ query: "search1", results: ["r1"], taskId: "task-1", sources: [] });
        expect(result[1]).toEqual({ query: "search2", results: ["r2"], taskId: "task-1", sources: [] });
    });

    test("extracts RAG data from tasks with JSON string metadata", () => {
        const tasks = [
            {
                taskId: "task-2",
                taskMetadata: JSON.stringify({ ragData: [{ query: "q1" }] }),
            },
        ];
        const result = extractRagDataFromTasks(tasks);
        expect(result).toHaveLength(1);
        expect(result[0].taskId).toBe("task-2");
    });

    test("prefers workflowTaskId over taskId over id", () => {
        const tasks = [
            { workflowTaskId: "wf-1", taskId: "task-1", id: "id-1", taskMetadata: { ragData: [{ q: 1 }] } },
            { taskId: "task-2", id: "id-2", taskMetadata: { ragData: [{ q: 2 }] } },
            { id: "id-3", taskMetadata: { ragData: [{ q: 3 }] } },
        ];
        const result = extractRagDataFromTasks(tasks);
        expect(result[0].taskId).toBe("wf-1");
        expect(result[1].taskId).toBe("task-2");
        expect(result[2].taskId).toBe("id-3");
    });

    test("skips tasks with no ragData", () => {
        const tasks = [
            { taskId: "t1", taskMetadata: { feedback: { type: "up" } } },
            { taskId: "t2", taskMetadata: null },
            { taskId: "t3", taskMetadata: { ragData: [{ q: "found" }] } },
        ];
        const result = extractRagDataFromTasks(tasks);
        expect(result).toHaveLength(1);
        expect(result[0].taskId).toBe("t3");
    });

    test("skips tasks with invalid JSON metadata", () => {
        const tasks = [{ taskId: "t1", taskMetadata: "{bad json}" }];
        const result = extractRagDataFromTasks(tasks);
        expect(result).toEqual([]);
    });

    test("skips tasks where ragData is not an array", () => {
        const tasks = [{ taskId: "t1", taskMetadata: { ragData: "not-array" as unknown as unknown[] } }];
        const result = extractRagDataFromTasks(tasks);
        expect(result).toEqual([]);
    });
});
