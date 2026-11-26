// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { type Connector, ConnectorType } from "@/types";

import { fetchAPIConnector, isSpecificationLoadedFromUrl } from "./fetchConnector";
import { getResource, invalidateResource, type Resource } from "./resourcesCache";

const spec = vi.hoisted(() => ({ getSpecification: vi.fn() }));

vi.mock("@/api/connectors/versioned", async importOriginal => {
    const actual = await importOriginal<typeof import("@/api/connectors/versioned")>();
    return { ...actual, connectorsApi: { ...actual.connectorsApi, getSpecification: spec.getSpecification } };
});
vi.mock("@/features/connectors/utils/resourcesCache", () => ({ getResource: vi.fn(), invalidateResource: vi.fn() }));

const SAVED_ID = "7c9e6679-7425-40de-944b-e07fc1f90ae7";
const SPEC_URL = "https://example.test/specs/petstore.json";
const SPEC_BODY = '{"openapi":"3.0.0"}';
const YAML_SPEC_BODY = "openapi: 3.0.0\ninfo:\n  title: Pets\n";

class FakeDataTransfer {
    private readonly stored: File[] = [];
    readonly files = this.stored as unknown as FileList;
    readonly items = { add: (file: File) => this.stored.push(file) };
}

function savedConnector(values: Record<string, unknown>): Connector {
    return { id: SAVED_ID, name: "petstore", type: ConnectorType.Api, subtype: "openapi", values } as unknown as Connector;
}

function resourceResponse(status: number, overrides: Partial<Resource> = {}): Resource {
    return { url: "", contentType: "application/json", text: "", status, statusText: status === 404 ? "Not Found" : "Server Error", ...overrides };
}

function readText(file: File): Promise<string> {
    return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result));
        reader.onerror = () => reject(reader.error);
        reader.readAsText(file);
    });
}

function attachedFile(connector: Connector): File {
    expect(connector.resources).toHaveLength(1);
    return connector.resources![0];
}

describe("fetchAPIConnector", () => {
    const getResourceMock = vi.mocked(getResource);
    const invalidateResourceMock = vi.mocked(invalidateResource);
    const getSpecificationMock = spec.getSpecification;
    const fetchMock = vi.fn();

    beforeEach(() => {
        getSpecificationMock.mockReset();
        getResourceMock.mockReset();
        invalidateResourceMock.mockReset();
        fetchMock.mockReset();
        vi.stubGlobal("fetch", fetchMock);
        vi.stubGlobal("DataTransfer", FakeDataTransfer);
    });

    afterEach(() => {
        vi.unstubAllGlobals();
    });

    it("attaches the stored resource and never touches the specification URL when the platform holds one", async () => {
        getResourceMock.mockResolvedValue(resourceResponse(200, { text: SPEC_BODY }));

        const { connector, error } = await fetchAPIConnector(savedConnector({ specification_url: SPEC_URL }));

        expect(error).toBeUndefined();
        const file = attachedFile(connector);
        expect(file.name).toBe("petstore-spec.json");
        expect(file.type).toBe("application/json");
        expect(isSpecificationLoadedFromUrl(file)).toBe(false);
        expect(fetchMock).not.toHaveBeenCalled();
    });

    it("loads the spec through the platform when a saved connector has no stored resource", async () => {
        getResourceMock.mockResolvedValue(resourceResponse(404));
        getSpecificationMock.mockResolvedValue({ content: SPEC_BODY, format: "json" });

        const { connector, error } = await fetchAPIConnector(savedConnector({ specification_url: SPEC_URL }));

        expect(error).toBeUndefined();
        expect(getSpecificationMock).toHaveBeenCalledWith(SAVED_ID);
        const file = attachedFile(connector);
        expect(file.name).toBe("petstore-spec.json");
        expect(file.type).toBe("application/json");
        expect(await readText(file)).toBe(SPEC_BODY);
        expect(isSpecificationLoadedFromUrl(file)).toBe(true);
    });

    it("never reads the external specification URL from the browser", async () => {
        getResourceMock.mockResolvedValue(resourceResponse(404));
        getSpecificationMock.mockResolvedValue({ content: SPEC_BODY, format: "json" });

        await fetchAPIConnector(savedConnector({ specification_url: SPEC_URL }));

        expect(fetchMock).not.toHaveBeenCalled();
    });

    it("types a YAML spec by the format the platform reports so the form parses it as YAML", async () => {
        getResourceMock.mockResolvedValue(resourceResponse(404));
        getSpecificationMock.mockResolvedValue({ content: YAML_SPEC_BODY, format: "yaml" });

        const { connector, error } = await fetchAPIConnector(savedConnector({ specification_url: "https://example.test/v3/api-docs" }));

        expect(error).toBeUndefined();
        const file = attachedFile(connector);
        expect(file.name).toBe("petstore-spec.yaml");
        expect(file.type.endsWith("yaml")).toBe(true);
    });

    it.each([
        `/api/v1/platform/connectors/${SAVED_ID}/resource`,
        `https://platform.example.test/api/v1/platform/connectors/${SAVED_ID}/resource`,
        `/api/v2/platform/connectors/${SAVED_ID}/resource`,
        `https://platform.example.test/api/v2/platform/connectors/${SAVED_ID}/resource`,
    ])("does not ask the platform to read its own display path %s as if it were an external spec URL", async displayPath => {
        getResourceMock.mockResolvedValue(resourceResponse(404));

        const { error } = await fetchAPIConnector(savedConnector({ specification_url: displayPath }));

        expect(error).toBe("Unable to fetch OpenAPI specification: Not Found");
        expect(getSpecificationMock).not.toHaveBeenCalled();
    });

    it.each(["ftp://example.test/petstore.json", "data:application/json,{}", "file:///tmp/petstore.json"])("does not ask the platform to read the non-http specification_url %s", async nonHttpUrl => {
        getResourceMock.mockResolvedValue(resourceResponse(404));

        const { error } = await fetchAPIConnector(savedConnector({ specification_url: nonHttpUrl }));

        expect(error).toBe("The connector's specification URL is not an http:// or https:// URL. Correct it, or upload the specification file.");
        expect(getSpecificationMock).not.toHaveBeenCalled();
    });

    it("names the URL without its query or credentials in the load error", async () => {
        getResourceMock.mockResolvedValue(resourceResponse(404));
        getSpecificationMock.mockRejectedValue(new Error("boom"));

        const { error } = await fetchAPIConnector(savedConnector({ specification_url: "https://user:pw@example.test/specs/petstore.json?token=abc123" }));

        expect(error).toBe("Unable to read the OpenAPI specification from https://example.test/specs/petstore.json: boom");
    });

    it.each(["", "   "])("does not call an empty specification_url %j unusable", async emptyUrl => {
        getResourceMock.mockResolvedValue(resourceResponse(404));

        const { error } = await fetchAPIConnector(savedConnector({ specification_url: emptyUrl }));

        expect(error).toBe("Unable to fetch OpenAPI specification: Not Found");
        expect(getSpecificationMock).not.toHaveBeenCalled();
    });

    it("does not report an unusable specification_url when the stored resource loaded", async () => {
        getResourceMock.mockResolvedValue(resourceResponse(200, { text: SPEC_BODY }));

        const { connector, error } = await fetchAPIConnector(savedConnector({ specification_url: "ftp://example.test/petstore.json" }));

        expect(error).toBeUndefined();
        expect(attachedFile(connector).name).toBe("petstore-spec.json");
    });

    it("does not ask the platform to read a URL whose scheme it does not treat as external", async () => {
        getResourceMock.mockResolvedValue(resourceResponse(404));

        const { error } = await fetchAPIConnector(savedConnector({ specification_url: "HTTPS://example.test/specs/petstore.json" }));

        expect(error).toBe("The connector's specification URL is not an http:// or https:// URL. Correct it, or upload the specification file.");
        expect(getSpecificationMock).not.toHaveBeenCalled();
    });

    it("names the platform's reason when it cannot read the specification URL", async () => {
        getResourceMock.mockResolvedValue(resourceResponse(404));
        getSpecificationMock.mockRejectedValue(new Error("The specification URL did not return an OpenAPI document."));

        const { connector, error } = await fetchAPIConnector(savedConnector({ specification_url: SPEC_URL }));

        expect(error).toBe("Unable to read the OpenAPI specification from https://example.test/specs/petstore.json: The specification URL did not return an OpenAPI document.");
        expect(connector.resources).toBeUndefined();
    });

    it("reads the stored resource fresh instead of trusting a lookup cached earlier in the session", async () => {
        getResourceMock.mockResolvedValue(resourceResponse(404));

        await fetchAPIConnector(savedConnector({}));

        expect(invalidateResourceMock).toHaveBeenCalledWith(`/api/v2/platform/connectors/${SAVED_ID}/resource`);
        expect(invalidateResourceMock.mock.invocationCallOrder[0]).toBeLessThan(getResourceMock.mock.invocationCallOrder[0]);
    });

    it("says so when the stored specification is empty", async () => {
        getResourceMock.mockResolvedValue(resourceResponse(200, { text: "", statusText: "OK" }));

        const { error } = await fetchAPIConnector(savedConnector({}));

        expect(error).toBe("Unable to fetch OpenAPI specification: the stored specification is empty");
    });

    it("reports the missing resource when a saved connector has neither a stored resource nor a specification_url", async () => {
        getResourceMock.mockResolvedValue(resourceResponse(404));

        const { error } = await fetchAPIConnector(savedConnector({}));

        expect(error).toBe("Unable to fetch OpenAPI specification: Not Found");
        expect(fetchMock).not.toHaveBeenCalled();
    });

    it("keeps the error for a real resource fetch failure instead of falling back to the URL", async () => {
        getResourceMock.mockResolvedValue(resourceResponse(500));

        const { error } = await fetchAPIConnector(savedConnector({ specification_url: SPEC_URL }));

        expect(error).toBe("Unable to fetch OpenAPI specification: Server Error");
        expect(fetchMock).not.toHaveBeenCalled();
    });

    it("names the status when the platform answers without a status text", async () => {
        getResourceMock.mockResolvedValue(resourceResponse(502, { statusText: "" }));

        const { error } = await fetchAPIConnector(savedConnector({ specification_url: SPEC_URL }));

        expect(error).toBe("Unable to fetch OpenAPI specification: HTTP 502");
    });

    it("keeps the error when the resource request itself throws", async () => {
        getResourceMock.mockRejectedValue(new Error("network down"));

        const { error } = await fetchAPIConnector(savedConnector({ specification_url: SPEC_URL }));

        expect(error).toContain("Error fetching OpenAPI specification");
        expect(fetchMock).not.toHaveBeenCalled();
    });
});
