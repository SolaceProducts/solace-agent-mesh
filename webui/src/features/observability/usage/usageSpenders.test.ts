// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { usageRow } from "@/testing/data/usage";

import { displayName, SPENDERS_SHOWN } from "./usageKinds";
import { hasBreakdownFigures, otherSpend, outsideSpend, SPENDER_COLORS, spenderColors } from "./usageSpenders";

const rows = [usageRow({ name: "A", cost: "260.85", activities: 1 }), usageRow({ name: "B", cost: "195.40", activities: 1 })];

describe("otherSpend", () => {
    test("is the total minus the listed rows, in exact money, with the share they leave", () => {
        const other = otherSpend(rows, "1240.5800000000");
        expect(other?.cost).toBe("784.3300000000");
        expect(other?.share).toBeCloseTo(100 - rows[0].shareOfSpend! - rows[1].shareOfSpend!, 10);
    });

    test("is null when the listed rows are the whole total", () => {
        expect(otherSpend(rows, "456.2500000000")).toBeNull();
    });

    test("draws no Other from a malformed amount rather than throwing during render", () => {
        expect(otherSpend(rows, "NaN")).toBeNull();
    });

    test("never goes negative when the listed shares round past 100", () => {
        const over = [
            { ...rows[0], shareOfSpend: 70 },
            { ...rows[1], shareOfSpend: 40 },
        ];
        expect(otherSpend(over, "1240.5800000000")?.share).toBe(0);
    });

    test("is null rather than negative when the listed rows cost more than the kind's total", () => {
        expect(otherSpend(rows, "400.0000000000")).toBeNull();
    });

    describe("with the other spenders card's rows", () => {
        const onlyOther = usageRow({ name: "C", cost: "100", activities: 1 });

        test("leaves out the other card's rows as well as its own", () => {
            const other = otherSpend(rows, "1240.5800000000", [onlyOther]);
            expect(other?.cost).toBe("684.3300000000");
            expect(other?.share).toBeCloseTo(100 - rows[0].shareOfSpend! - rows[1].shareOfSpend! - onlyOther.shareOfSpend!, 10);
        });

        test("takes out a row both cards show only once", () => {
            expect(otherSpend(rows, "1240.5800000000", [rows[0], onlyOther])?.cost).toBe("684.3300000000");
        });

        test("is null when the two cards together show every row, as when there are five or fewer", () => {
            expect(otherSpend([rows[1]], "456.2500000000", rows)).toBeNull();
        });

        test("does not repeat one big spender in Lowest Spenders' Other", () => {
            const orchestrator = usageRow({ name: "Orchestrator", cost: "46.46", activities: 120 });
            const idle = Array.from({ length: SPENDERS_SHOWN + 1 }, (_, i) => usageRow({ name: `Idle ${i}`, cost: "0", activities: 0 }));
            const top = [orchestrator, ...idle.slice(0, SPENDERS_SHOWN - 1)];
            const lowest = idle.slice(1);
            expect(otherSpend(lowest, "46.4600000000", top)).toBeNull();
        });

        describe("a row with no id or name, as an unknown user's", () => {
            const unnamed = { ...onlyOther, id: null, bookedName: null, name: null };

            test("is left out when this card lists no such row, so it can't be one of this card's", () => {
                expect(otherSpend(rows, "1240.5800000000", [unnamed])?.cost).toBe("684.3300000000");
            });

            test("stays in Other when this card lists one too, since the two can't be told apart", () => {
                const listedUnnamed = { ...unnamed, cost: "10.0000000000" as const };
                expect(otherSpend([...rows, listedUnnamed], "1240.5800000000", [unnamed])?.cost).toBe("774.3300000000");
            });
        });
    });
});

describe("outsideSpend", () => {
    test("is the total minus the kind's cost, in exact money", () => {
        expect(outsideSpend("1240.5800000000", "767.3400000000")).toBe("473.2400000000");
    });

    test("is null when the kind's cost is the whole total", () => {
        expect(outsideSpend("1240.5800000000", "1240.5800000000")).toBeNull();
    });

    test("is null for a malformed amount rather than throwing during render", () => {
        expect(outsideSpend("NaN", "767.3400000000")).toBeNull();
        expect(outsideSpend("1240.5800000000", "")).toBeNull();
    });
});

describe("hasBreakdownFigures", () => {
    test("needs both amounts to be readable money", () => {
        expect(hasBreakdownFigures("767.3400000000", "1240.5800000000")).toBe(true);
        expect(hasBreakdownFigures("NaN", "1240.5800000000")).toBe(false);
        expect(hasBreakdownFigures("767.3400000000", "")).toBe(false);
    });
});

describe("spenderColors", () => {
    const [big, mid, small] = [usageRow({ name: "Big", cost: "30", activities: 1 }), usageRow({ name: "Mid", cost: "20", activities: 1 }), usageRow({ name: "Small", cost: "10", activities: 1 })];

    test("a subject in both lists keeps its top rank's colour in the lowest list", () => {
        const colorFor = spenderColors([big, mid, small], [small, mid, big]);
        expect([small, mid, big].map((row, index) => colorFor(row, index))).toEqual([SPENDER_COLORS[2], SPENDER_COLORS[1], SPENDER_COLORS[0]]);
    });

    test("a full Lowest Spenders card of lowest-only subjects repeats no colour", () => {
        const lowest = Array.from({ length: SPENDERS_SHOWN }, (_, i) => usageRow({ name: `Idle ${i}`, cost: "0", activities: 0 }));
        const colorFor = spenderColors([big], lowest);
        expect(new Set(lowest.map((row, index) => colorFor(row, index))).size).toBe(SPENDERS_SHOWN);
    });
});

describe("displayName", () => {
    test("names a user row with no id Unknown user, never its sign-in subject", () => {
        expect(displayName("users", { ...rows[0], id: null, name: "google-oauth2|1" })).toBe("Unknown user");
    });

    test("names a user row whose id no users row has Unknown user", () => {
        expect(displayName("users", { ...rows[0], name: null, user: null })).toBe("Unknown user");
    });

    test("keeps the name of a deleted system user, which the server still sends", () => {
        expect(displayName("users", { ...rows[0], name: "Nightly Pipeline", user: null })).toBe("Nightly Pipeline");
    });

    test("names a known user with no name with a dash", () => {
        expect(displayName("users", { ...rows[0], name: null, user: { email: "a@example.com", isSystemUser: false } })).toBe("—");
    });

    test("keeps the name of an agent, workflow or model with no id", () => {
        expect(displayName("workflows", { ...rows[0], id: null, name: "nightly-report" })).toBe("nightly-report");
    });
});
