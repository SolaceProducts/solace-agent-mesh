// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useId, useState } from "react";
import { CalendarRange, Check, ChevronDown } from "lucide-react";

import { Button } from "@/ui/button";
import { DatePicker } from "@/ui/date-picker";
import { FIELD_TRIGGER } from "@/ui/field-trigger";
import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";

import { cn } from "@/utils";

import { type DateRange, formatRangeLabel } from "./dashboardFilters";
import { MAX_WINDOW_DAYS, spanDays } from "./retentionLimit";

type PresetOption<P extends string> = { value: P; label: string };

const ROW_CLASS = "flex w-full items-center justify-between px-3 py-1.5 text-left text-sm outline-none hover:bg-(--primary-w10) focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-(--brand-wMain)";

const MISSING_DATES = "Pick a start and end date.";

function rejectionFor(from: string, to: string, maxSpanDays: number): string | null {
    if (from > to) return "The start date must come before the end date.";
    if (spanDays(from, to) > maxSpanDays) return `A range can cover at most ${maxSpanDays} days.`;
    return null;
}

export interface DateRangeFilterProps<P extends string> {
    value: DateRange<P>;
    onChange: (range: DateRange<P>) => void;
    presets: ReadonlyArray<PresetOption<P>>;
    minDate?: string;
    maxDate: string;
    maxSpanDays?: number;
    label?: string;
    labelledBy?: string;
}

export function DateRangeFilter<P extends string>({ value, onChange, presets, minDate, maxDate, maxSpanDays = MAX_WINDOW_DAYS, label = "Date Range", labelledBy }: Readonly<DateRangeFilterProps<P>>) {
    const [open, setOpen] = useState(false);
    const [showCustom, setShowCustom] = useState(value.kind === "custom");
    const [from, setFrom] = useState(value.kind === "custom" ? value.from : "");
    const [to, setTo] = useState(value.kind === "custom" ? value.to : "");
    const [triedApply, setTriedApply] = useState(false);

    const appliedFrom = value.kind === "custom" ? value.from : "";
    const appliedTo = value.kind === "custom" ? value.to : "";

    useEffect(() => {
        if (!appliedFrom || !appliedTo) return;
        setShowCustom(true);
        setFrom(appliedFrom);
        setTo(appliedTo);
    }, [appliedFrom, appliedTo]);

    const missing = !from || !to;
    const rejection = missing ? null : rejectionFor(from, to, maxSpanDays);
    const message = missing && triedApply ? MISSING_DATES : rejection;
    const rejectionId = useId();
    const valueId = useId();

    const choosePreset = (preset: P) => {
        setShowCustom(false);
        setTriedApply(false);
        setOpen(false);
        onChange({ kind: "preset", preset });
    };

    const apply = () => {
        if (missing) {
            setTriedApply(true);
            return;
        }
        if (rejection) return;
        setOpen(false);
        onChange({ kind: "custom", from, to });
    };

    return (
        <Popover
            open={open}
            onOpenChange={isOpen => {
                setOpen(isOpen);
                if (!isOpen) setTriedApply(false);
            }}
        >
            <PopoverTrigger asChild>
                <Button variant="outline" aria-label={labelledBy ? undefined : label} aria-labelledby={labelledBy ? `${labelledBy} ${valueId}` : undefined} className={cn(FIELD_TRIGGER, "h-auto w-64 px-3 py-2 text-(--primary-text-wMain)")}>
                    <span className="flex min-w-0 items-center gap-2">
                        <CalendarRange className="size-4 shrink-0 text-(--secondary-text-wMain)" aria-hidden />
                        <span id={valueId} className="truncate">
                            {formatRangeLabel(value, presets)}
                        </span>
                    </span>
                    <ChevronDown className="size-4 shrink-0 text-(--secondary-text-wMain)" aria-hidden />
                </Button>
            </PopoverTrigger>
            <PopoverContent className="w-72 p-0" align="start">
                <div className="flex flex-col py-1">
                    {presets.map(preset => {
                        const isActive = value.kind === "preset" && value.preset === preset.value;
                        return (
                            <button key={preset.value} type="button" aria-current={isActive ? "true" : undefined} onClick={() => choosePreset(preset.value)} className={cn(ROW_CLASS, isActive && "font-medium text-(--primary-text-wMain)")}>
                                {preset.label}
                                {isActive && <Check className="size-4 shrink-0" aria-hidden />}
                            </button>
                        );
                    })}
                </div>
                <div className="border-t">
                    <button type="button" aria-current={value.kind === "custom" ? "true" : undefined} onClick={() => setShowCustom(true)} className={cn(ROW_CLASS, value.kind === "custom" && "font-medium text-(--primary-text-wMain)")}>
                        Custom Range
                        {value.kind === "custom" && <Check className="size-4 shrink-0" aria-hidden />}
                    </button>
                    {showCustom && (
                        <div className="flex flex-col gap-2 px-3 pt-1 pb-3">
                            <label className="flex items-center gap-2 text-xs text-(--secondary-text-wMain)">
                                <span className="w-8 shrink-0">From</span>
                                <DatePicker value={from} onChange={setFrom} min={minDate} max={maxDate} ariaLabel="Start date" placeholder="Start date" />
                            </label>
                            <label className="flex items-center gap-2 text-xs text-(--secondary-text-wMain)">
                                <span className="w-8 shrink-0">To</span>
                                <DatePicker value={to} onChange={setTo} min={minDate} max={maxDate} ariaLabel="End date" placeholder="End date" />
                            </label>
                            <p id={rejectionId} aria-live="polite" className={cn("text-xs text-(--error-wMain)", !message && "sr-only")}>
                                {message}
                            </p>
                            <Button onClick={apply} disabled={Boolean(rejection)} aria-describedby={message ? rejectionId : undefined} className="self-end">
                                Apply
                            </Button>
                        </div>
                    )}
                </div>
            </PopoverContent>
        </Popover>
    );
}
