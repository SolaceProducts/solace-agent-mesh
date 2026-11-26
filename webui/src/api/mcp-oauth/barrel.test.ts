// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import * as barrel from "./index";
import * as v2 from "./v2";
import * as versioned from "./versioned";

const WHY =
    "api/mcp-oauth/index.ts must not re-export v2.ts or the registry built from it (name check only; reachability through hooks.ts is accepted): the vitest setup evaluates barrels with the real API client before vi.mock registers. Import it directly.";

describe("api/mcp-oauth barrel", () => {
    it.each([
        ["v2", v2],
        ["versioned", versioned],
    ])("does not export anything from %s", (_name, module) => {
        const exported = new Set(Object.keys(barrel));
        expect(Object.keys(module).length).toBeGreaterThan(0);
        expect(
            Object.keys(module).filter(name => exported.has(name)),
            WHY
        ).toEqual([]);
    });
});
