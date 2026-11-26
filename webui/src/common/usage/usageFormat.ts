// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const MS_PER_MINUTE = 60_000;
const MINUTES_PER_HOUR = 60;
const HOURS_PER_DAY = 24;

const TOKEN_COUNT = new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 });
const WHOLE_THOUSANDS = new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 0 });
const TENS_OF_THOUSANDS = 10_000;
const MILLION = 1_000_000;
const STAMP_LABEL = new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", timeZone: "UTC", timeZoneName: "short" });

const OLDER_USAGE_CALCULATING = "Older usage is still being calculated, so totals may be low.";
const USAGE_CALCULATING = "Usage is still being calculated, so totals may be low.";

export function formatDataAsOf(dataAsOf: string, now: Date): string {
    const at = Date.parse(dataAsOf);
    if (Number.isNaN(at)) return "Update time unknown";
    const minutes = Math.floor((now.getTime() - at) / MS_PER_MINUTE);
    if (minutes < 1) return "Updated just now";
    if (minutes < MINUTES_PER_HOUR) return `Updated ${minutes} min ago`;
    const hours = Math.floor(minutes / MINUTES_PER_HOUR);
    if (hours < HOURS_PER_DAY) return `Updated ${hours} h ago`;
    return `Updated ${STAMP_LABEL.format(new Date(dataAsOf))}`;
}

export function formatFreshness(dataAsOf: string | null, historyComplete: boolean, now: Date): string {
    if (dataAsOf === null) return USAGE_CALCULATING;
    const updated = formatDataAsOf(dataAsOf, now);
    return historyComplete ? updated : `${updated}. ${OLDER_USAGE_CALCULATING}`;
}

export function formatCompactTokens(value: number): string {
    return value >= TENS_OF_THOUSANDS && value < MILLION ? WHOLE_THOUSANDS.format(value) : TOKEN_COUNT.format(value);
}
