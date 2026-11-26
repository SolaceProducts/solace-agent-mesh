// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";

import { downloadBlob } from "@/utils";

import { downloadStim } from "./downloadStim";

vi.mock("@/api/client", () => ({ api: { webui: { get: vi.fn() } } }));
vi.mock("@/utils", () => ({ downloadBlob: vi.fn() }));

const getMock = vi.mocked(api.webui.get);
const downloadBlobMock = vi.mocked(downloadBlob);

function notifiers() {
    return { addNotification: vi.fn(), displayError: vi.fn() };
}

describe("downloadStim", () => {
    afterEach(() => vi.clearAllMocks());

    test("downloads the blob as {taskId}.stim and notifies success", async () => {
        const blob = new Blob(["log"]);
        getMock.mockResolvedValue({ ok: true, blob: () => Promise.resolve(blob) } as unknown as Response);
        const n = notifiers();

        await downloadStim("task-123", n);

        expect(getMock).toHaveBeenCalledWith("/api/v1/tasks/task-123", { fullResponse: true });
        expect(downloadBlobMock).toHaveBeenCalledWith(blob, "task-123.stim");
        expect(n.addNotification).toHaveBeenCalledWith("Task log downloaded", "success");
        expect(n.displayError).not.toHaveBeenCalled();
    });

    test("surfaces the server's message on a refused response and does not download", async () => {
        getMock.mockResolvedValue({ ok: false, status: 404, statusText: "", json: () => Promise.resolve({ message: "task not found" }) } as unknown as Response);
        const n = notifiers();

        await downloadStim("task-x", n);

        expect(downloadBlobMock).not.toHaveBeenCalled();
        expect(n.addNotification).not.toHaveBeenCalled();
        expect(n.displayError).toHaveBeenCalledTimes(1);
        expect(n.displayError.mock.calls[0][0].title).toBe("Failed to Download Task Log");
        expect(n.displayError.mock.calls[0][0].error).toBe("Failed to download task log: task not found");
    });

    test("falls back to the status code when a refused response carries no message", async () => {
        getMock.mockResolvedValue({ ok: false, status: 502, statusText: "", json: () => Promise.reject(new Error("not json")) } as unknown as Response);
        const n = notifiers();

        await downloadStim("task-z", n);

        expect(n.displayError.mock.calls[0][0].error).toBe("Failed to download task log: HTTP 502");
    });

    test("surfaces an error when the request throws", async () => {
        getMock.mockRejectedValue(new Error("network down"));
        const n = notifiers();

        await downloadStim("task-y", n);

        expect(downloadBlobMock).not.toHaveBeenCalled();
        expect(n.displayError).toHaveBeenCalledTimes(1);
    });
});
