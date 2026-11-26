// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { Controller, useFormContext, useWatch } from "react-hook-form";

import { FormFieldLayoutItem, PageLabel } from "@/common/layout";

import { CustomCheckbox, HelpPopover } from "@/common";
import { Input } from "@/ui";

import {
    CACHE_RATES,
    cachePricesStated,
    cacheRateRequiredBecauseOf,
    cacheRatesBlockedByMissingRequiredRates,
    currencySymbol,
    formatRateForInput,
    INPUT_RATE,
    OUTPUT_RATE,
    PRICE_CURRENCY,
    rateFieldLabel,
    rateFormatError,
    type RateKey,
    rateRequiredBecauseOf,
    REQUIRED_RATES,
} from "./modelPricing";
import type { ModelFormData } from "./modelProviderUtils";

const RateInput: React.FC<{
    rateKey: RateKey;
    label: string;
    invalid: boolean;
    requiredBy: { label: string } | null;
    blockedReason?: string | null;
}> = ({ rateKey, label, invalid, requiredBy, blockedReason }) => {
    const { register, getValues, setValue } = useFormContext<ModelFormData>();
    const field = `pricing.${rateKey}` as const;

    const { onBlur: registeredOnBlur, ...registration } = register(field, {
        validate: value => {
            const formatError = rateFormatError(value as string | undefined);
            if (formatError) return formatError;
            const text = ((value as string | undefined) ?? "").trim();
            if (blockedReason && text !== "") return blockedReason;
            if (requiredBy && text === "") {
                return `Required once ${requiredBy.label.toLowerCase()} is priced`;
            }
            return true;
        },
    });

    return (
        <div className="relative">
            <span className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-sm text-(--secondary-text-wMain)">{currencySymbol()}</span>
            <Input
                id={`pricing-${rateKey}`}
                className="pl-7"
                inputMode="decimal"
                aria-label={label}
                aria-invalid={invalid}
                aria-required={!!requiredBy}
                aria-describedby={rateMessageId(rateKey)}
                {...registration}
                onBlur={async event => {
                    const formatted = formatRateForInput(getValues(field));
                    if (formatted !== getValues(field)) {
                        setValue(field, formatted, { shouldDirty: true });
                    }
                    await registeredOnBlur(event);
                }}
            />
        </div>
    );
};

function rateMessageId(rateKey: RateKey): string {
    return `pricing-${rateKey}-message`;
}

const INPUT_HINT_CACHE_INCLUDED = "Tokens sent to the model, including cached tokens";
const INPUT_HINT_NO_CACHE = "Tokens sent to the model";

export const ModelPricingFields = ({ offersCacheRates = true }: Readonly<{ offersCacheRates?: boolean }>) => {
    const {
        control,
        formState: { errors },
    } = useFormContext<ModelFormData>();

    const pricing = useWatch({ control, name: "pricing" });
    const entered: Partial<Record<RateKey, string | undefined>> = {
        input: pricing?.input,
        output: pricing?.output,
        cacheRead: pricing?.cacheRead,
        cacheWrite: pricing?.cacheWrite,
    };

    const rateErrors = errors.pricing as Partial<Record<RateKey, { message?: string }>> | undefined;

    const blockedReason = cacheRatesBlockedByMissingRequiredRates(entered) ? "Price input and output before adding cache prices" : null;

    const requiredRateField = (rate: (typeof REQUIRED_RATES)[number], helpText: string) => {
        const requiredBy = rateRequiredBecauseOf(rate.key, entered);
        return (
            <FormFieldLayoutItem key={rate.key} label={rateFieldLabel(rate.label)} required={!!requiredBy} error={rateErrors?.[rate.key]} helpText={helpText} messageId={rateMessageId(rate.key)}>
                <RateInput rateKey={rate.key} label={rateFieldLabel(rate.label)} invalid={!!rateErrors?.[rate.key]} requiredBy={requiredBy} />
            </FormFieldLayoutItem>
        );
    };
    const inputField = requiredRateField(INPUT_RATE, inputRateHint(offersCacheRates, cachePricesStated(pricing)));
    const outputField = requiredRateField(OUTPUT_RATE, OUTPUT_RATE.hint);

    return (
        <div className="mt-4 flex flex-col gap-6" data-testid="model-pricing-fields">
            <div className="flex flex-col gap-1">
                <PageLabel>Pricing</PageLabel>
                <p>What this model costs, in {PRICE_CURRENCY}. Leave input and output blank to leave the model unpriced; enter 0 in both for a model that costs nothing.</p>
            </div>

            {offersCacheRates ? (
                <>
                    <div className="grid grid-cols-1 gap-6 sm:grid-cols-2">
                        {inputField}
                        <CacheRatesToggle />
                    </div>

                    {pricing?.breakOutCacheRates && (
                        <div className="grid grid-cols-1 gap-6 sm:grid-cols-2" data-testid="model-pricing-cache-rates">
                            {CACHE_RATES.map(rate => {
                                const requiredBy = blockedReason ? null : cacheRateRequiredBecauseOf(rate.key, entered);
                                return (
                                    <FormFieldLayoutItem key={rate.key} label={rateFieldLabel(rate.label)} required={!!requiredBy} error={rateErrors?.[rate.key]} helpText={rate.hint} messageId={rateMessageId(rate.key)}>
                                        <RateInput rateKey={rate.key} label={rateFieldLabel(rate.label)} invalid={!!rateErrors?.[rate.key]} requiredBy={requiredBy} blockedReason={blockedReason} />
                                    </FormFieldLayoutItem>
                                );
                            })}
                        </div>
                    )}

                    <div className="grid grid-cols-1 gap-6 sm:grid-cols-2">{outputField}</div>
                </>
            ) : (
                <div className="grid grid-cols-1 gap-6 sm:grid-cols-2">
                    {inputField}
                    {outputField}
                </div>
            )}
        </div>
    );
};

function inputRateHint(offersCacheRates: boolean, cachePricesAreStated: boolean): string {
    if (!offersCacheRates) return INPUT_HINT_NO_CACHE;
    return cachePricesAreStated ? INPUT_RATE.hint : INPUT_HINT_CACHE_INCLUDED;
}

const CacheRatesToggle = () => {
    const { control } = useFormContext<ModelFormData>();
    return (
        <Controller
            control={control}
            name="pricing.breakOutCacheRates"
            render={({ field }) => (
                <div className="flex h-9 items-center gap-2 sm:mt-7">
                    <label className="flex w-fit cursor-pointer items-center gap-2 text-sm" htmlFor="pricing-break-out">
                        <CustomCheckbox id="pricing-break-out" checked={!!field.value} onChange={event => field.onChange(event.target.checked)} />
                        Cached input tokens have different prices
                    </label>
                    <HelpPopover label="Cached input token prices help" content="If this is left unchecked, cache read and cache write prices default to the input price." side="top" size="16" />
                </div>
            )}
        />
    );
};
