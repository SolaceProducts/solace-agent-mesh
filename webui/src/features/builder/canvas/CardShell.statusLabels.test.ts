// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { deriveLabelText, deriveStatusKind } from "./CardShell";

describe("deriveLabelText", () => {
    test("an explicit statusText always wins", () => {
        expect(deriveLabelText({ statusText: "Waiting for Input...", generating: true })).toBe("Waiting for Input...");
    });

    test("the build-phase sub-states all read as a single 'Creating' pill", () => {
        expect(deriveLabelText({ generating: true })).toBe("Creating");
        expect(deriveLabelText({ buildingDeps: true })).toBe("Creating");
        expect(deriveLabelText({ queued: true })).toBe("Creating");
    });

    test("editing wins over generating", () => {
        expect(deriveLabelText({ editing: true, generating: true })).toBe("Editing");
    });

    test("idle lifecycle states", () => {
        expect(deriveLabelText({ draft: true })).toBe("Draft");
        expect(deriveLabelText({ ready: true })).toBe("Ready");
        expect(deriveLabelText({ active: true })).toBe("Active");
        expect(deriveLabelText({ error: true })).toBe("Error");
    });

    test("doneLabel overrides the terminal Ready/Active copy (skills read 'Available')", () => {
        expect(deriveLabelText({ ready: true, doneLabel: "Available" })).toBe("Available");
        expect(deriveLabelText({ active: true, doneLabel: "Available" })).toBe("Available");
        expect(deriveLabelText({ generating: true, doneLabel: "Available" })).toBe("Creating");
        expect(deriveLabelText({ draft: true, doneLabel: "Available" })).toBe("Draft");
    });
});

describe("deriveStatusKind", () => {
    test("maps each flag to its visual kind (icon/color), with precedence", () => {
        expect(deriveStatusKind({ generating: true })).toBe("generating");
        expect(deriveStatusKind({ buildingDeps: true })).toBe("buildingDeps");
        expect(deriveStatusKind({ queued: true })).toBe("queued");
        expect(deriveStatusKind({ draft: true })).toBe("draft");
        expect(deriveStatusKind({ done: true })).toBe("done");
        expect(deriveStatusKind({ error: true })).toBe("error");
        expect(deriveStatusKind({ isWaiting: true })).toBe("waiting");
        expect(deriveStatusKind({ error: true, generating: true })).toBe("error");
        expect(deriveStatusKind({ isWaiting: true, generating: true })).toBe("waiting");
    });

    test("returns null when no status flag is set", () => {
        expect(deriveStatusKind({})).toBeNull();
    });
});
