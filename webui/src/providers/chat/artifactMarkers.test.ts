// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it, vi } from "vitest";

import { removeArtifactMarkers, stripVersionSuffix } from "./artifactMarkers";

describe("removeArtifactMarkers", () => {
    it("removes an artifact_return marker", () => {
        expect(removeArtifactMarkers("Done. «artifact_return:a.txt»")).toBe("Done. ");
    });

    it("removes an artifact marker", () => {
        expect(removeArtifactMarkers("Done. «artifact:a.txt»")).toBe("Done. ");
    });

    it("passes the unversioned filename of a versioned marker to the predicate", () => {
        const shouldRemove = vi.fn(() => true);

        expect(removeArtifactMarkers("Done. «artifact_return:a.txt:3»", shouldRemove)).toBe("Done. ");
        expect(shouldRemove).toHaveBeenCalledWith("a.txt");
    });

    it("removes a marker wrapped in triple guillemets", () => {
        expect(removeArtifactMarkers("Done. «««artifact_return:a.txt»»»")).toBe("Done. ");
    });

    it("keeps a marker the predicate rejects", () => {
        const text = "Done. «artifact_return:a.txt» «artifact_return:b.txt»";

        expect(removeArtifactMarkers(text, name => name === "b.txt")).toBe("Done. «artifact_return:a.txt» ");
    });
});

describe("stripVersionSuffix", () => {
    it("strips only a trailing numeric version segment", () => {
        expect(stripVersionSuffix("a:b:2")).toBe("a:b");
    });

    it("leaves an unversioned filename unchanged", () => {
        expect(stripVersionSuffix("a.txt")).toBe("a.txt");
    });

    it("leaves a non-numeric trailing segment unchanged", () => {
        expect(stripVersionSuffix("a:1x")).toBe("a:1x");
    });
});
