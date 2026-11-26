// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Connector } from "@/types";

describe("connector service specification_url absolutizing", () => {
    let absolutizeSpecURL: typeof import("./service").absolutizeSpecURL;
    let platformOrigin: ReturnType<typeof vi.fn>;
    const connector = (values: Record<string, unknown>) => ({ id: "c1", values }) as unknown as Connector;

    beforeEach(async () => {
        vi.resetModules();
        platformOrigin = vi.fn(() => "https://platform.example.com");
        vi.doMock("@/api/client", () => ({ api: { platformOrigin } }));
        ({ absolutizeSpecURL } = await import("./service"));
    });

    afterEach(() => {
        vi.doUnmock("@/api/client");
    });

    it.each(["/api/v2/platform/connectors/c1/resource", "/api/v1/platform/connectors/c1/resource"])("prefixes the relative specification_url %s with the platform origin", storedPath => {
        const absolutized = absolutizeSpecURL(connector({ specification_url: storedPath }));
        expect(absolutized.values.specification_url).toBe(`https://platform.example.com${storedPath}`);
    });

    it("leaves an absolute specification_url unchanged", () => {
        const absolutized = absolutizeSpecURL(connector({ specification_url: "https://api.example.com/spec.yaml" }));
        expect(absolutized.values.specification_url).toBe("https://api.example.com/spec.yaml");
    });

    it("leaves a connector without specification_url unchanged", () => {
        const absolutized = absolutizeSpecURL(connector({ base_url: "https://api.example.com" }));
        expect(absolutized.values.specification_url).toBeUndefined();
        expect(platformOrigin).not.toHaveBeenCalled();
    });
});
