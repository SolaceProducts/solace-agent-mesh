// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, test, vi } from "vitest";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { downloadStim } from "./downloadStim";
import { StimDownloadButton } from "./StimDownloadButton";

vi.mock("./downloadStim", () => ({ downloadStim: vi.fn() }));
const downloadStimMock = vi.mocked(downloadStim);

function renderButton(taskId: string | null | undefined, taskLogging: boolean, unavailableReason?: string) {
    return render(
        <StoryProvider flagValues={{ task_logging: taskLogging }}>
            <StimDownloadButton taskId={taskId} unavailableReason={unavailableReason} />
        </StoryProvider>
    );
}

describe("StimDownloadButton", () => {
    afterEach(() => vi.clearAllMocks());

    test("renders nothing when task logging is disabled", () => {
        renderButton("t1", false);
        expect(screen.queryByRole("button")).toBeNull();
    });

    test("is disabled and does not download when there is no taskId", async () => {
        renderButton(undefined, true);
        const btn = screen.getByRole("button");
        expect(btn).toBeDisabled();
        await userEvent.click(btn);
        expect(downloadStimMock).not.toHaveBeenCalled();
    });

    test("stays focusable but inert, with the reason as its tooltip, while the details are unavailable", async () => {
        renderButton("task-123", true, "Task details are not available.");
        const btn = screen.getByRole("button", { name: "Download Task Log (.stim)" });
        expect(btn).toHaveAttribute("aria-disabled", "true");
        expect(btn).not.toBeDisabled();
        await userEvent.click(btn);
        expect(downloadStimMock).not.toHaveBeenCalled();
    });

    test("downloads the task's stim when enabled and clicked", async () => {
        renderButton("task-123", true);
        const btn = screen.getByRole("button");
        expect(btn).not.toBeDisabled();
        await userEvent.click(btn);
        expect(downloadStimMock).toHaveBeenCalledWith("task-123", expect.objectContaining({ addNotification: expect.any(Function), displayError: expect.any(Function) }));
    });
});
