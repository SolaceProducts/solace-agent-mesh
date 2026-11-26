// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function formatUsd(amount: string): string {
    const [whole, fraction = ""] = amount.split(".");
    return `$${groupThousands(whole)}.${fraction.padEnd(2, "0")}`;
}

export const AMOUNT_PATTERN = /^\d+(\.\d{1,2})?$/;

export function toCents(amount: string): bigint {
    const [whole, frac = ""] = amount.split(".");
    return BigInt(whole) * 100n + BigInt(frac.padEnd(2, "0"));
}

export function formatBudgetLimit(amount: string | null): string {
    return amount === null ? "Unlimited" : formatUsd(amount);
}

function groupThousands(digits: string): string {
    const groups: string[] = [];
    for (let end = digits.length; end > 0; end -= 3) {
        groups.unshift(digits.slice(Math.max(0, end - 3), end));
    }
    return groups.join(",");
}

const TOP_UP_EXPIRY_FORMAT = new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", year: "numeric", hour: "2-digit", minute: "2-digit", timeZone: "UTC", timeZoneName: "short" });

export function formatTopUpExpiry(expiresTime: string): string {
    return TOP_UP_EXPIRY_FORMAT.format(new Date(expiresTime));
}
