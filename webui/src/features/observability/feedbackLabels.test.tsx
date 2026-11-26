// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { entrypointDisplay, formatFeedbackTime, formatFeedbackTimeWithYear, RatingBadge, subjectLabel } from "./feedbackLabels";

describe("subjectLabel", () => {
    it("prefers the resolved name", () => {
        expect(subjectLabel({ subjectType: "agent", subjectName: "Agent One", subjectId: "a-1" })).toBe("Agent One");
    });

    it("falls back to the id when the name is unresolved", () => {
        expect(subjectLabel({ subjectType: "agent", subjectName: "", subjectId: "a-1" })).toBe("a-1");
    });

    it("renders the migration sentinel as Unknown", () => {
        expect(subjectLabel({ subjectType: "unknown", subjectName: "", subjectId: "unknown" })).toBe("Unknown");
    });
});

describe("entrypointDisplay", () => {
    it.each([
        ["webui", "Web UI"],
        ["slack", "Slack"],
        ["teams_bot", "Microsoft Teams"],
        ["whatsapp", "WhatsApp"],
        ["google_chat", "Google Chat"],
    ])("maps %s to %s with an icon", (type, label) => {
        const display = entrypointDisplay(type);
        expect(display.label).toBe(label);
        expect(display.Icon).not.toBeNull();
    });

    it.each(["unknown", "carrier_pigeon", ""])("maps %j to Unknown with no icon", type => {
        expect(entrypointDisplay(type)).toEqual({ label: "Unknown", Icon: null });
    });
});

describe("formatFeedbackTime", () => {
    afterEach(() => {
        vi.unstubAllEnvs();
    });

    it("drops the year and the seconds", () => {
        vi.stubEnv("TZ", "UTC");

        const formatted = formatFeedbackTime("2026-08-25T09:42:17.000Z");

        expect(formatted).toContain("Aug");
        expect(formatted).toContain("25");
        expect(formatted).toContain("9:42");
        expect(formatted).not.toContain("2026");
        expect(formatted).not.toContain(":17");
    });

    it("renders the instant on the viewer's own clock", () => {
        vi.stubEnv("TZ", "Asia/Tokyo");

        expect(formatFeedbackTime("2026-08-25T09:42:00.000Z")).toContain("6:42");
    });
});

describe("formatFeedbackTimeWithYear", () => {
    afterEach(() => {
        vi.unstubAllEnvs();
    });

    it("adds the year the compact rendering drops", () => {
        vi.stubEnv("TZ", "UTC");

        const formatted = formatFeedbackTimeWithYear("2026-08-25T09:42:17.000Z");

        expect(formatted).toContain("2026");
        expect(formatted).toContain("Aug");
        expect(formatted).toContain("25");
        expect(formatted).toContain("9:42");
    });

    it("differs from the compact rendering only by the year", () => {
        vi.stubEnv("TZ", "UTC");

        const iso = "2026-08-25T09:42:00.000Z";

        expect(formatFeedbackTimeWithYear(iso)).not.toBe(formatFeedbackTime(iso));
        expect(formatFeedbackTime(iso)).not.toContain("2026");
    });

    it("renders the instant on the viewer's own clock", () => {
        vi.stubEnv("TZ", "Asia/Tokyo");

        expect(formatFeedbackTimeWithYear("2026-08-25T09:42:00.000Z")).toContain("6:42");
    });
});

describe("RatingBadge", () => {
    it("labels a positive rating", () => {
        render(<RatingBadge rating="up" />);
        expect(screen.getByLabelText("Positive")).toBeInTheDocument();
    });

    it("labels a negative rating", () => {
        render(<RatingBadge rating="down" />);
        expect(screen.getByLabelText("Negative")).toBeInTheDocument();
    });

    it.each([
        ["up", "Positive"],
        ["down", "Negative"],
    ] as const)("sizes the %s glyph at 14px, displacing the badge default", (rating, label) => {
        render(<RatingBadge rating={rating} />);

        const glyphSizes = Array.from(screen.getByLabelText(label).classList).filter(name => name.startsWith("[&>svg]:size-"));

        expect(glyphSizes).toEqual(["[&>svg]:size-3.5"]);
    });
});
