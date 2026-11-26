// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/client";

import { type Connector, ConnectorType } from "@/types";

import { fetchAPIConnector } from "./fetchConnector";
import { ConnectorResourceUploadError, saveConnector } from "./saveConnector";

vi.mock("@/api/client", () => ({
    api: { platform: { get: vi.fn() } },
}));

const h = vi.hoisted(() => ({ getSpecification: vi.fn(), createFromForm: vi.fn(), update: vi.fn(), delete: vi.fn(), uploadResource: vi.fn() }));

vi.mock("@/api/connectors/versioned", async importOriginal => {
    const actual = await importOriginal<typeof import("@/api/connectors/versioned")>();
    return { ...actual, connectorsApi: { ...actual.connectorsApi, ...h } };
});

const SAVED_ID = "7c9e6679-7425-40de-944b-e07fc1f90ae7";
const SPEC_URL = "https://example.test/specs/petstore.json";

function fileList(file: File): FileList {
    return [file] as unknown as FileList;
}

function editedConnector(resources: FileList): Connector {
    return { id: SAVED_ID, name: "petstore", description: "d", type: ConnectorType.Api, subtype: "openapi", values: { specification_url: SPEC_URL }, resources } as unknown as Connector;
}

const URL_SPEC = '{"openapi":"3.0.0"}';

function respondNoStoredResourceAndSpecFromUrl() {
    vi.mocked(api.platform.get).mockResolvedValue({ ok: false, status: 404, statusText: "Not Found", headers: new Headers(), text: async () => "" } as Response);
    h.getSpecification.mockResolvedValue({ content: URL_SPEC, format: "json" });
}

describe("saveConnector with a specification read from specification_url", () => {
    beforeEach(() => {
        h.update.mockReset();
        h.uploadResource.mockReset();
        vi.mocked(api.platform.get).mockReset();
    });

    afterEach(() => {
        vi.unstubAllGlobals();
    });

    it("does not upload the URL-loaded spec as a stored resource, so the connector stays URL-backed", async () => {
        vi.stubGlobal(
            "DataTransfer",
            class {
                files: File[] = [];
                items = { add: (f: File) => this.files.push(f) };
            }
        );
        respondNoStoredResourceAndSpecFromUrl();
        const loaded = await fetchAPIConnector({ id: SAVED_ID, name: "petstore", type: ConnectorType.Api, subtype: "openapi", values: { specification_url: SPEC_URL } } as unknown as Connector);

        expect(loaded.error).toBeUndefined();
        expect(loaded.connector.resources).toHaveLength(1);
        expect(loaded.connector.resources?.[0].type).toBe("application/json");

        await saveConnector(loaded.connector, false, SAVED_ID);

        expect(h.uploadResource).not.toHaveBeenCalled();
        expect(h.update).toHaveBeenCalledTimes(1);
        expect(h.update).toHaveBeenCalledWith(SAVED_ID, expect.objectContaining({ values: expect.objectContaining({ specification_url: SPEC_URL }) }));
    });

    it("uploads a spec the user picked", async () => {
        const picked = new File(['{"openapi":"3.0.1"}'], "mine.json", { type: "application/json" });

        await saveConnector(editedConnector(fileList(picked)), false, SAVED_ID);

        expect(h.uploadResource).toHaveBeenCalledWith(SAVED_ID, expect.any(FormData));
    });
});

describe("saveConnector when the resource upload fails", () => {
    const uploadError = new Error("upload rejected");
    const picked = () => fileList(new File(['{"openapi":"3.0.1"}'], "mine.json", { type: "application/json" }));

    beforeEach(() => {
        h.createFromForm.mockReset().mockResolvedValue({ id: SAVED_ID });
        h.update.mockReset();
        h.delete.mockReset().mockResolvedValue(undefined);
        h.uploadResource.mockReset().mockRejectedValue(uploadError);
        vi.spyOn(console, "error").mockImplementation(() => undefined);
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    it("deletes a newly created connector and rethrows the upload error", async () => {
        await expect(saveConnector(editedConnector(picked()), true, undefined)).rejects.toBe(uploadError);

        expect(h.delete).toHaveBeenCalledTimes(1);
        expect(h.delete).toHaveBeenCalledWith(SAVED_ID);
    });

    it("still rethrows the upload error when the rollback delete also fails", async () => {
        h.delete.mockRejectedValue(new Error("delete failed"));

        await expect(saveConnector(editedConnector(picked()), true, undefined)).rejects.toBe(uploadError);
    });

    it("keeps an existing connector and throws the upload error with the warnings of the save", async () => {
        const warning =
            'approval gates on "list_pets" could not be checked: the connector has no uploaded specification and none could be read from its specification URL, so a gate on an operation the specification lacks stops the agents bound to this connector from starting, or is not applied when an agent selects operations with an allow_list';
        h.update.mockResolvedValue({ id: SAVED_ID, warnings: [warning] });

        const failure = await saveConnector(editedConnector(picked()), false, SAVED_ID).catch((error: unknown) => error);

        expect(failure).toBeInstanceOf(ConnectorResourceUploadError);
        expect((failure as ConnectorResourceUploadError).warnings).toEqual([warning]);
        expect((failure as ConnectorResourceUploadError).message).toBe("upload rejected");
        expect((failure as ConnectorResourceUploadError).cause).toBe(uploadError);
        expect(h.delete).not.toHaveBeenCalled();
    });
});

describe("saveConnector warnings", () => {
    const removedGate = 'approval gate on "delete_pet" was removed: the connector no longer provides that tool';
    const unchecked = 'approval gates on "list_pets" could not be checked';
    const noSpecUnchecked =
        'approval gates on "list_pets" could not be checked: the connector has no uploaded specification and none could be read from its specification URL, so a gate on an operation the specification lacks stops the agents bound to this connector from starting, or is not applied when an agent selects operations with an allow_list';

    beforeEach(() => {
        h.createFromForm.mockReset();
        h.update.mockReset();
        h.uploadResource.mockReset();
    });

    it("returns the warnings of a create", async () => {
        h.createFromForm.mockResolvedValue({ id: SAVED_ID, warnings: [unchecked] });

        const saved = await saveConnector(editedConnector(undefined as unknown as FileList), true, undefined);

        expect(saved).toEqual({ id: SAVED_ID, warnings: [unchecked] });
    });

    it("returns the warnings of the PATCH and of the spec upload together", async () => {
        h.update.mockResolvedValue({ id: SAVED_ID, warnings: [unchecked] });
        h.uploadResource.mockResolvedValue({ id: SAVED_ID, warnings: [removedGate] });
        const picked = new File(['{"openapi":"3.0.1"}'], "mine.json", { type: "application/json" });

        const saved = await saveConnector(editedConnector(fileList(picked)), false, SAVED_ID);

        expect(saved).toEqual({ id: SAVED_ID, warnings: [unchecked, removedGate] });
    });

    it("drops the unchecked-gates warning when the same save uploads the spec", async () => {
        h.update.mockResolvedValue({ id: SAVED_ID, warnings: [noSpecUnchecked, removedGate] });
        h.uploadResource.mockResolvedValue({ id: SAVED_ID });
        const picked = new File(['{"openapi":"3.0.1"}'], "mine.json", { type: "application/json" });

        const saved = await saveConnector(editedConnector(fileList(picked)), false, SAVED_ID);

        expect(saved.warnings).toEqual([removedGate]);
    });

    it("drops the unparseable-spec warning when the same save uploads a new spec", async () => {
        const unparseable =
            'approval gates on "list_pets" could not be checked: the uploaded specification could not be parsed, so a gate on an operation the specification lacks stops the agents bound to this connector from starting, or is not applied when an agent selects operations with an allow_list';
        h.update.mockResolvedValue({ id: SAVED_ID, warnings: [unparseable] });
        h.uploadResource.mockResolvedValue({ id: SAVED_ID });
        const picked = new File(['{"openapi":"3.0.1"}'], "mine.json", { type: "application/json" });

        const saved = await saveConnector(editedConnector(fileList(picked)), false, SAVED_ID);

        expect(saved.warnings).toEqual([]);
    });

    it("keeps the unchecked-gates warning when the save uploads no spec", async () => {
        h.update.mockResolvedValue({ id: SAVED_ID, warnings: [noSpecUnchecked] });

        const saved = await saveConnector(editedConnector(undefined as unknown as FileList), false, SAVED_ID);

        expect(saved.warnings).toEqual([noSpecUnchecked]);
    });

    it("updates with only the name, description and values", async () => {
        h.update.mockResolvedValue({ id: SAVED_ID });

        await saveConnector(editedConnector(undefined as unknown as FileList), false, SAVED_ID);

        expect(h.update).toHaveBeenCalledWith(SAVED_ID, { name: "petstore", description: "d", values: { specification_url: SPEC_URL } });
    });

    it("returns no warnings when the platform sends none", async () => {
        h.update.mockResolvedValue(undefined);

        const saved = await saveConnector(editedConnector(undefined as unknown as FileList), false, SAVED_ID);

        expect(saved).toEqual({ id: SAVED_ID, warnings: [] });
    });
});
