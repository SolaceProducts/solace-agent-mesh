// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it, vi } from "vitest";

import { deployOneComponent } from "./useManifestDeployment";

describe("deployOneComponent", () => {
    const kinds = () => ({
        agent: vi.fn(async () => undefined),
        workflow: vi.fn(async () => undefined),
        gateway: vi.fn(async () => undefined),
    });
    const component = (type: string) => ({ name: "c", type, id: "id-1" }) as Parameters<typeof deployOneComponent>[0];

    it.each([
        ["agent", "agent"],
        ["a2a_agent", "agent"],
        ["workflow", "workflow"],
        ["gateway", "gateway"],
    ])("deploys a %s through its own mutation", async (type, expected) => {
        const k = kinds();
        await expect(deployOneComponent(component(type), k)).resolves.toBe(true);
        for (const [name, fn] of Object.entries(k)) {
            expect(fn, `${name} called for a ${type}`).toHaveBeenCalledTimes(name === expected ? 1 : 0);
        }
        expect(k[expected as keyof typeof k]).toHaveBeenCalledWith("id-1");
    });

    it("counts a connector as deployed without deploying anything", async () => {
        const k = kinds();
        await expect(deployOneComponent(component("connector"), k)).resolves.toBe(true);
        expect(Object.values(k).every(fn => fn.mock.calls.length === 0)).toBe(true);
    });

    it("reports a kind it does not deploy as not deployed", async () => {
        const k = kinds();
        await expect(deployOneComponent(component("skill"), k)).resolves.toBe(false);
        expect(Object.values(k).every(fn => fn.mock.calls.length === 0)).toBe(true);
    });
});
