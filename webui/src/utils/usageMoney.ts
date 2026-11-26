// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageMoney } from "@/api/usage";

const SCALE_DIGITS = 10;
const SCALE = 10n ** BigInt(SCALE_DIGITS);
const MONEY_PATTERN = /^(-)?(\d+)(?:\.(\d+))?$/;

const WHOLE_FORMAT = new Intl.NumberFormat("en-US");

export function isUsageMoney(amount: string): amount is UsageMoney {
    return MONEY_PATTERN.test(amount);
}

export function parseUsageMoney(amount: string): bigint {
    const match = MONEY_PATTERN.exec(amount);
    if (!match) throw new Error(`Not a usage money amount: ${amount}`);
    const [, sign, whole, fraction = ""] = match;
    const kept = BigInt(fraction.slice(0, SCALE_DIGITS).padEnd(SCALE_DIGITS, "0"));
    const roundsUp = fraction.length > SCALE_DIGITS && fraction[SCALE_DIGITS] >= "5";
    const scaled = BigInt(whole) * SCALE + kept + (roundsUp ? 1n : 0n);
    return sign ? -scaled : scaled;
}

const UNREADABLE = "—";

export function toUsageMoney(scaled: bigint): UsageMoney {
    const negative = scaled < 0n;
    const magnitude = negative ? -scaled : scaled;
    const fraction = (magnitude % SCALE).toString().padStart(SCALE_DIGITS, "0");
    return `${negative ? "-" : ""}${magnitude / SCALE}.${fraction}` as UsageMoney;
}

export function sumUsageMoney(amounts: readonly string[]): UsageMoney {
    return toUsageMoney(amounts.reduce((total, amount) => total + parseUsageMoney(amount), 0n));
}

export function remainderUsageMoney(minuend: string, subtrahends: readonly string[]): UsageMoney {
    const remainder = parseUsageMoney(minuend) - parseUsageMoney(sumUsageMoney(subtrahends));
    return toUsageMoney(remainder < 0n ? 0n : remainder);
}

export function compareUsageMoney(a: string, b: string): number {
    const diff = parseUsageMoney(a) - parseUsageMoney(b);
    if (diff === 0n) return 0;
    return diff < 0n ? -1 : 1;
}

export function isZeroUsageMoney(amount: string): boolean {
    return MONEY_PATTERN.test(amount) && parseUsageMoney(amount) === 0n;
}

function placesFor(decimals: number): number {
    return Math.min(Math.max(Math.trunc(decimals), 0), SCALE_DIGITS);
}

function stepFor(decimals: number): bigint {
    return 10n ** BigInt(SCALE_DIGITS - placesFor(decimals));
}

export function formatUsageMoney(amount: string, decimals = 2): string {
    if (!MONEY_PATTERN.test(amount)) return UNREADABLE;
    const places = placesFor(decimals);
    const scaled = parseUsageMoney(amount);
    const negative = scaled < 0n;
    const magnitude = negative ? -scaled : scaled;
    const step = stepFor(places);
    const rounded = (magnitude + step / 2n) / step;
    const unit = 10n ** BigInt(places);
    const whole = WHOLE_FORMAT.format(rounded / unit);
    const fraction = places > 0 ? `.${(rounded % unit).toString().padStart(places, "0")}` : "";
    return `${negative && rounded !== 0n ? "-" : ""}$${whole}${fraction}`;
}

export function formatUsageMoneyWithFloor(amount: string, decimals: number): string {
    if (isUsageMoney(amount)) {
        const scaled = parseUsageMoney(amount);
        const step = stepFor(decimals);
        if (scaled > 0n && scaled < step / 2n) return `<${formatUsageMoney(toUsageMoney(step), decimals)}`;
    }
    return formatUsageMoney(amount, decimals);
}

export function isRoundedUsageMoney(amount: string, decimals: number): boolean {
    return isUsageMoney(amount) && parseUsageMoney(amount) % stepFor(decimals) !== 0n;
}

export function formatExactUsageMoney(amount: string): string {
    if (!isUsageMoney(amount)) return UNREADABLE;
    let places = 2;
    while (isRoundedUsageMoney(amount, places)) places++;
    return formatUsageMoney(amount, places);
}

export function formatCostPerActivity(amount: string | null): string {
    return amount === null ? "—" : formatUsageMoney(amount, 3);
}

export function formatBudgetMoney(amount: string): string {
    const cents = formatUsageMoney(amount, 2);
    return cents.endsWith(".00") ? cents.slice(0, -3) : cents;
}

export function usageMoneyToNumber(amount: string): number {
    return isUsageMoney(amount) ? Number(amount) : 0;
}

const PERCENT_EPSILON = 1e-9;

export function formatUsagePercent(percent: number): string {
    const tenths = Math.trunc(percent * 10 + Math.sign(percent) * PERCENT_EPSILON);
    return `${(tenths / 10).toFixed(1)}%`;
}
