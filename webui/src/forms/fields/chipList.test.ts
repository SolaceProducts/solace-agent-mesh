// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { addEntries, joinPaste } from "./chipList";

const SPLIT = /[,\s]+/;
const parse = (raw: string) => (raw === "bad" ? { value: "bad", problem: "nope" } : { value: raw.toLowerCase() });

describe("addEntries", () => {
    test("splits pasted text, normalizes and ignores duplicates", () => {
        expect(addEntries(["a"], "A, b  b\nc", parse, SPLIT)).toEqual(["a", "b", "c"]);
    });

    test("keeps invalid entries as chips", () => {
        expect(addEntries(["a"], "b bad c", parse, SPLIT)).toEqual(["a", "b", "bad", "c"]);
    });

    test("ignores empty input", () => {
        expect(addEntries(["a"], " , ", parse, SPLIT)).toEqual(["a"]);
    });
});

describe("joinPaste", () => {
    test("inserts a separator between a finished draft and the paste", () => {
        expect(joinPaste("a.com", "b.org")).toBe("a.com,b.org");
    });

    test("treats a paste starting with @ as a new entry, so the draft is not fused into an address", () => {
        expect(joinPaste("a.com", "@b.org")).toBe("a.com,@b.org");
    });

    test("concatenates when the paste continues the draft", () => {
        expect(joinPaste("example", ".com")).toBe("example.com");
        expect(joinPaste("solace", ", b.org")).toBe("solace, b.org");
        expect(joinPaste("a.com ", "b.org")).toBe("a.com b.org");
    });

    test("is just the paste when the draft is empty", () => {
        expect(joinPaste("", "b.org")).toBe("b.org");
    });
});
