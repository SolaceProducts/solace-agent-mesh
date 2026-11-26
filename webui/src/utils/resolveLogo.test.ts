// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { resolveLogo } from "./resolveLogo";

const LARGE = "https://cdn.example.com/large.svg";
const SMALL = "https://cdn.example.com/small.svg";

describe("resolveLogo", () => {
    test("a configured url is drawn", () => {
        expect(resolveLogo(LARGE, null)).toEqual({ kind: "image", url: LARGE });
    });

    test("unconfigured falls through to the other slot", () => {
        expect(resolveLogo(null, SMALL)).toEqual({ kind: "image", url: SMALL });
    });

    test("unconfigured on both sides draws the default mark", () => {
        expect(resolveLogo(null, null)).toEqual({ kind: "default" });
        expect(resolveLogo(undefined, undefined)).toEqual({ kind: "default" });
    });

    test("an empty slot draws the default mark, never nothing", () => {
        expect(resolveLogo("", null)).toEqual({ kind: "default" });
        expect(resolveLogo("", "")).toEqual({ kind: "default" });
    });

    test("an empty slot falls through to the other slot", () => {
        expect(resolveLogo("", SMALL)).toEqual({ kind: "image", url: SMALL });
        expect(resolveLogo(LARGE, "")).toEqual({ kind: "image", url: LARGE });
    });

    test("undefined behaves like unconfigured", () => {
        expect(resolveLogo(undefined, SMALL)).toEqual({ kind: "image", url: SMALL });
    });
});
