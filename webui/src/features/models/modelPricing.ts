// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ModelPricing } from "@/api/models/types";

export const PRICE_TOKEN_QUANTITY: number = 1_000_000;
export const PRICE_DECIMAL_PLACES = 4;

export const PRICE_MAX_AMOUNT = 1_000_000;

export const REQUIRED_RATES = [
    { key: "input", label: "Input", hint: "Tokens sent to the model, excluding cached tokens" },
    { key: "output", label: "Output", hint: "Tokens the model generates, including reasoning tokens" },
] as const;

export const [INPUT_RATE, OUTPUT_RATE] = REQUIRED_RATES;

export const CACHE_RATES = [
    { key: "cacheRead", label: "Cache Read", hint: "Tokens sent to the model that are read from the cache" },
    { key: "cacheWrite", label: "Cache Write", hint: "Tokens sent to the model that are written to the cache" },
] as const;

export const ALL_RATES = [...REQUIRED_RATES, ...CACHE_RATES];

export type RequiredRateKey = (typeof REQUIRED_RATES)[number]["key"];
export type CacheRateKey = (typeof CACHE_RATES)[number]["key"];
export type RateKey = RequiredRateKey | CacheRateKey;

const DECIMAL_AMOUNT = /^(\d+(\.\d*)?|\.\d+)$/;

export const PRICE_CURRENCY = "USD";
const PRICE_LOCALE = "en-US";

export function currencySymbol(): string {
    const parts = new Intl.NumberFormat(PRICE_LOCALE, { style: "currency", currency: PRICE_CURRENCY }).formatToParts(0);
    return parts.find(part => part.type === "currency")?.value ?? PRICE_CURRENCY;
}

export function rateUnitLabel(): string {
    return `per ${tokenQuantityLabel()} tokens`;
}

export function rateFieldLabel(label: string): string {
    return `${label} Price / ${tokenQuantityLabel()} Tokens`;
}

export function tokenQuantityLabel(): string {
    return PRICE_TOKEN_QUANTITY === 1_000_000 ? "1M" : PRICE_TOKEN_QUANTITY.toLocaleString();
}

export function rateFormatError(value: string | undefined): string | null {
    const text = (value ?? "").trim();
    if (text === "") return null;

    if (!DECIMAL_AMOUNT.test(text)) {
        return `Enter an amount, for example ${formatRate("3")}`;
    }
    const decimals = text.split(".")[1]?.length ?? 0;
    if (decimals > PRICE_DECIMAL_PLACES) {
        return `At most ${PRICE_DECIMAL_PLACES} decimal places`;
    }
    if (Number(text) > PRICE_MAX_AMOUNT) {
        return `At most ${PRICE_MAX_AMOUNT.toLocaleString(PRICE_LOCALE)} ${rateUnitLabel()}`;
    }
    return null;
}

export function rateRequiredBecauseOf(key: RequiredRateKey, data: Partial<Record<RateKey, string | undefined>>): (typeof REQUIRED_RATES)[number] | null {
    const counterpart = key === "input" ? REQUIRED_RATES[1] : REQUIRED_RATES[0];
    return filled(data[counterpart.key]) ? counterpart : null;
}

export function cacheRateRequiredBecauseOf(key: CacheRateKey, data: Partial<Record<RateKey, string | undefined>>): (typeof CACHE_RATES)[number] | null {
    const counterpart = key === "cacheRead" ? CACHE_RATES[1] : CACHE_RATES[0];
    return filled(data[counterpart.key]) ? counterpart : null;
}

export function cacheRatesBlockedByMissingRequiredRates(data: Partial<Record<RateKey, string | undefined>>): boolean {
    return !filled(data.input) || !filled(data.output);
}

export interface PricingFormValues {
    input: string;
    output: string;
    cacheRead: string;
    cacheWrite: string;
    breakOutCacheRates: boolean;
}

export const EMPTY_PRICING_FORM: PricingFormValues = {
    input: "",
    output: "",
    cacheRead: "",
    cacheWrite: "",
    breakOutCacheRates: false,
};

export function readPricingFromForm(pricing: PricingFormValues | undefined): ModelPricing {
    const breakOut = pricing?.breakOutCacheRates ?? false;

    return {
        input: trimmedOrNull(pricing?.input),
        output: trimmedOrNull(pricing?.output),
        cacheRead: breakOut ? trimmedOrNull(pricing?.cacheRead) : null,
        cacheWrite: breakOut ? trimmedOrNull(pricing?.cacheWrite) : null,
    };
}

export function cachePricesStated(pricing: PricingFormValues | undefined): boolean {
    return !!pricing?.breakOutCacheRates && (filled(pricing.cacheRead) || filled(pricing.cacheWrite));
}

export function formatRateForInput(value: string | undefined): string {
    const text = (value ?? "").trim();
    if (text === "" || rateFormatError(text) !== null) return text;

    const [whole, fraction = ""] = text.split(".");
    return `${whole === "" ? "0" : whole}.${fraction.padEnd(2, "0")}`;
}

export function pricingFormValues(pricing: ModelPricing | null | undefined): PricingFormValues {
    if (!pricing) return EMPTY_PRICING_FORM;

    return {
        input: formatRateForInput(pricing.input ?? ""),
        output: formatRateForInput(pricing.output ?? ""),
        cacheRead: formatRateForInput(pricing.cacheRead ?? ""),
        cacheWrite: formatRateForInput(pricing.cacheWrite ?? ""),
        breakOutCacheRates: pricing.cacheRead != null || pricing.cacheWrite != null,
    };
}

export function isUnpriced(pricing: ModelPricing | null | undefined): boolean {
    return !pricing || ALL_RATES.every(rate => pricing[rate.key] == null);
}

export function rateDisplay(pricing: ModelPricing | null | undefined, key: RateKey): { amount: string; resolved: boolean } | null {
    if (isUnpriced(pricing) || !pricing) return null;

    const stated = pricing[key];
    if (stated != null) return { amount: formatRate(stated), resolved: false };

    if (key === "cacheRead" || key === "cacheWrite") {
        if (pricing.input == null) return null;
        return { amount: formatRate(pricing.input), resolved: true };
    }
    return null;
}

export function formatRate(amount: string): string {
    const value = Number(amount);
    if (!Number.isFinite(value)) return `${currencySymbol()}${amount}`;

    return value.toLocaleString(PRICE_LOCALE, {
        style: "currency",
        currency: PRICE_CURRENCY,
        minimumFractionDigits: 2,
        maximumFractionDigits: PRICE_DECIMAL_PLACES,
    });
}

function filled(value: string | undefined): boolean {
    return (value ?? "").trim() !== "";
}

function trimmedOrNull(value: string | undefined): string | null {
    const text = (value ?? "").trim();
    return text === "" ? null : text;
}
