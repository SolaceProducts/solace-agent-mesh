// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { breaches, type Build, eventsOf, type Model, type ModelEvent, start, step } from "@/testing/helpers/saveOnlyModel";

const depthOf = (build: Build) => (build === "both" ? 5 : 6);

const reachable = (build: Build, asks: boolean) => eventsOf(build).filter(event => asks || (event !== "yes" && event !== "no"));

describe("the save-only flow, every ordering of its events", () => {
    const starts = (["connector", "skill", "both"] as Build[]).flatMap(build => [true, false].flatMap(asks => [true, false].flatMap(recordKept => (asks ? [true, false] : [false]).map(owned => ({ build, asks, recordKept, owned })))));

    test.each(starts)("a $build build with asking=$asks, record kept=$recordKept, written by this session=$owned", ({ build, asks, recordKept, owned }) => {
        const failures: string[] = [];
        const fired = new Set<ModelEvent>();
        let sequences = 0;

        const walk = (m: Model, trail: ModelEvent[]) => {
            if (trail.length === depthOf(build)) return;
            for (const event of eventsOf(build)) {
                const next = step(m, event);
                if (!next) continue;
                fired.add(event);
                sequences++;
                const broken = breaches(m, event, next);
                if (broken.length > 0 && failures.length < 5) failures.push(`${[...trail, event].join(" > ")}: ${broken.join("; ")}`);
                walk(next, [...trail, event]);
            }
        };
        walk(start(build, asks, recordKept, owned), []);

        expect(failures).toEqual([]);
        expect([...fired].sort()).toEqual([...reachable(build, asks)].sort());
        expect(sequences).toBeGreaterThan(1000);
    });
});
