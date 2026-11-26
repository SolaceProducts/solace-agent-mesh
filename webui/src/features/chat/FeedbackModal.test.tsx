// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { FeedbackModal } from "./FeedbackModal";

const DATA_USE_NOTICE = "This conversation may be viewed to help improve responses";

const RETIRED_CONSENT_LABEL = "Attach this conversation to your feedback to help improve responses";

function renderModal(onSubmit = vi.fn().mockResolvedValue(undefined)) {
    render(<FeedbackModal isOpen onClose={vi.fn()} feedbackType="up" onSubmit={onSubmit} />);
    return onSubmit;
}

describe("FeedbackModal data use notice", () => {
    it("tells the user their conversation may be viewed", async () => {
        renderModal();

        expect(await screen.findByText(DATA_USE_NOTICE)).toBeInTheDocument();
    });

    it("offers no consent control to operate", async () => {
        renderModal();
        await screen.findByText(DATA_USE_NOTICE);

        expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
        expect(screen.queryByText(RETIRED_CONSENT_LABEL)).not.toBeInTheDocument();
    });
});

describe("FeedbackModal submission", () => {
    it("submits the comment and nothing else", async () => {
        const onSubmit = renderModal();
        fireEvent.change(await screen.findByRole("textbox"), { target: { value: "missed the breaking changes" } });

        fireEvent.click(screen.getByRole("button", { name: "Submit Feedback" }));

        await waitFor(() => expect(onSubmit).toHaveBeenCalledWith("missed the breaking changes"));
    });

    it("submits an empty comment as valid rating-only feedback", async () => {
        const onSubmit = renderModal();
        await screen.findByRole("textbox");

        fireEvent.click(screen.getByRole("button", { name: "Submit Feedback" }));

        await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(""));
    });
});
