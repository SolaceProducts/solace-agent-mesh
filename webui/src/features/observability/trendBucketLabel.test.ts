// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, describe, expect, it, vi } from "vitest";

import { formatBucketLabel } from "./trendBucketLabel";

describe("formatBucketLabel", () => {
    afterEach(() => {
        vi.unstubAllEnvs();
    });

    it("renders an hour bucket on the viewer's clock", () => {
        vi.stubEnv("TZ", "Asia/Tokyo");
        const label = formatBucketLabel("2026-09-01T18:00:00Z", "hour");
        expect(label).toMatch(/\b3:00\b/);
    });

    it("keeps a day bucket on its UTC calendar date west of UTC", () => {
        vi.stubEnv("TZ", "America/New_York");
        expect(formatBucketLabel("2026-09-01T00:00:00Z", "day")).toContain("Sep");
        expect(formatBucketLabel("2026-09-01T00:00:00Z", "day")).not.toContain("Aug");
    });

    it("keeps a week bucket on its UTC calendar date", () => {
        vi.stubEnv("TZ", "America/New_York");
        expect(formatBucketLabel("2026-08-31T00:00:00Z", "week")).toContain("Aug");
    });

    it("lets a DST fall-back repeat one hour's label rather than disambiguating it", () => {
        vi.stubEnv("TZ", "America/New_York");
        const start = Date.UTC(2026, 10, 1, 0, 0, 0);
        const labels = Array.from({ length: 24 }, (_, i) => formatBucketLabel(new Date(start + i * 3600000).toISOString(), "hour"));

        expect(labels).toHaveLength(24);

        const counts = new Map<string, number>();
        labels.forEach(label => counts.set(label, (counts.get(label) ?? 0) + 1));
        const repeated = [...counts.entries()].filter(([, count]) => count > 1);

        expect(repeated).toHaveLength(1);
        const [repeatedLabel, count] = repeated[0];
        expect(count).toBe(2);
        const indices = labels.reduce<number[]>((acc, label, i) => (label === repeatedLabel ? [...acc, i] : acc), []);
        expect(indices[1] - indices[0]).toBe(1);
    });

    it("returns the raw value rather than throwing on an unparseable bucket", () => {
        expect(formatBucketLabel("not-a-date", "day")).toBe("not-a-date");
    });
});
