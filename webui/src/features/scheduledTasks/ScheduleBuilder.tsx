// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useRef, useState } from "react";
import { Check, ChevronDown, CircleCheckBig, X } from "lucide-react";

import { MessageBanner } from "@/common";
import { Popover, PopoverAnchor, PopoverContent, PopoverTrigger, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui";
import { cn } from "@/utils";

import { cronToSchedule, DAY_LABELS, type FrequencyType, getScheduleDescription, MONTH_NTH_LABELS, type MonthlyMode, type MonthNth, type ScheduleConfig, scheduleToCron, validateCron } from "./schedule-cron";
import { describeScheduleExpression } from "./utils";

const DaysOfWeekPicker: React.FC<{ selected: number[]; onChange: (days: number[]) => void }> = ({ selected, onChange }) => {
    const [open, setOpen] = useState(false);
    const sorted = [...selected].sort((a, b) => a - b);
    const toggle = (idx: number) => {
        onChange(selected.includes(idx) ? selected.filter(d => d !== idx) : [...selected, idx]);
    };
    const remove = (idx: number) => {
        onChange(selected.filter(d => d !== idx));
    };

    return (
        <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger asChild>
                <button type="button" className="flex min-h-[2.5rem] w-full items-center gap-2 rounded-xs border bg-transparent px-2 py-1 text-left text-sm shadow-xs transition-colors outline-none focus-visible:border-(--brand-wMain)">
                    <div className="flex flex-1 flex-wrap items-center gap-1">
                        {sorted.length === 0 ? (
                            <span className="px-1 text-(--secondary-wMain)">Select days</span>
                        ) : (
                            sorted.map(idx => (
                                <span key={idx} className="inline-flex items-center gap-1 rounded-sm bg-(--secondary-w20) px-2 py-0.5 text-xs">
                                    {DAY_LABELS[idx]}
                                    <span
                                        role="button"
                                        tabIndex={0}
                                        onClick={e => {
                                            e.stopPropagation();
                                            remove(idx);
                                        }}
                                        onKeyDown={e => {
                                            if (e.key === "Enter" || e.key === " ") {
                                                e.preventDefault();
                                                e.stopPropagation();
                                                remove(idx);
                                            }
                                        }}
                                        aria-label={`Remove ${DAY_LABELS[idx]}`}
                                        className="cursor-pointer rounded-full p-0.5 hover:bg-(--secondary-w40)"
                                    >
                                        <X className="h-3 w-3" />
                                    </span>
                                </span>
                            ))
                        )}
                    </div>
                    <ChevronDown className="h-4 w-4 flex-shrink-0 opacity-50" />
                </button>
            </PopoverTrigger>
            <PopoverContent align="start" className="w-(--radix-popover-trigger-width) p-1">
                <div className="flex flex-col">
                    {DAY_LABELS.map((label, idx) => {
                        const isSelected = selected.includes(idx);
                        return (
                            <button key={idx} type="button" onClick={() => toggle(idx)} className={cn("flex items-center justify-between rounded-sm px-2 py-1.5 text-left text-sm hover:bg-(--secondary-w10)", isSelected && "font-medium")}>
                                <span>{label}</span>
                                {isSelected && <Check className="h-4 w-4 text-(--primary-wMain)" />}
                            </button>
                        );
                    })}
                </div>
            </PopoverContent>
        </Popover>
    );
};

function formatTime12h(hour24: number, minute: number): string {
    const period = hour24 >= 12 ? "PM" : "AM";
    const hour12 = hour24 % 12 || 12;
    return `${hour12}:${String(minute).padStart(2, "0")} ${period}`;
}

function parseTime12h(input: string): { hour24: number; minute: number } | null {
    const trimmed = input.trim().toUpperCase();
    const match = /^(\d{1,2}):(\d{1,2})\s*(AM|PM)$/.exec(trimmed);
    if (!match) return null;
    const h12 = parseInt(match[1], 10);
    const m = parseInt(match[2], 10);
    const period = match[3];
    if (h12 < 1 || h12 > 12 || m < 0 || m > 59) return null;
    let hour24 = h12 % 12;
    if (period === "PM") hour24 += 12;
    return { hour24, minute: m };
}

const TIME_OF_DAY_OPTIONS = Array.from({ length: 48 }, (_, i) => {
    const hour24 = Math.floor(i / 2);
    const minute = (i % 2) * 30;
    return { hour24, minute, label: formatTime12h(hour24, minute) };
});

export const TimeOfDayPicker: React.FC<{ value: string; onChange: (hhmm: string) => void; invalid?: boolean }> = ({ value, onChange, invalid }) => {
    const [open, setOpen] = useState(false);
    const [parts, setParts] = useState(() => value.split(":").map(Number));
    const initialDisplay = !isNaN(parts[0]) && !isNaN(parts[1]) ? formatTime12h(parts[0], parts[1]) : "9:00 AM";
    const [draft, setDraft] = useState(initialDisplay);
    const inputRef = useRef<HTMLInputElement>(null);

    useEffect(() => {
        const [h, m] = value.split(":").map(Number);
        if (!isNaN(h) && !isNaN(m)) {
            setParts([h, m]);
            setDraft(formatTime12h(h, m));
        }
    }, [value]);

    const commit = () => {
        const parsed = parseTime12h(draft);
        if (parsed) {
            const hhmm = `${String(parsed.hour24).padStart(2, "0")}:${String(parsed.minute).padStart(2, "0")}`;
            setDraft(formatTime12h(parsed.hour24, parsed.minute));
            if (hhmm !== value) onChange(hhmm);
        } else {
            const [h, m] = parts;
            setDraft(formatTime12h(Number.isFinite(h) ? h : 9, Number.isFinite(m) ? m : 0));
        }
    };

    const pick = (hour24: number, minute: number) => {
        const hhmm = `${String(hour24).padStart(2, "0")}:${String(minute).padStart(2, "0")}`;
        setDraft(formatTime12h(hour24, minute));
        setParts([hour24, minute]);
        onChange(hhmm);
        setOpen(false);
        inputRef.current?.blur();
    };

    return (
        <Popover open={open} onOpenChange={setOpen}>
            <PopoverAnchor asChild>
                <div className={`relative inline-flex w-[8rem] items-center rounded-xs border bg-transparent shadow-xs transition-colors focus-within:border-(--brand-wMain) ${invalid ? "border-(--error-w100)" : ""}`}>
                    <input
                        ref={inputRef}
                        type="text"
                        className="w-full bg-transparent py-2 pr-8 pl-3 text-sm outline-none"
                        value={draft}
                        onChange={e => setDraft(e.target.value)}
                        onBlur={commit}
                        onKeyDown={e => {
                            if (e.key === "Enter") {
                                e.preventDefault();
                                commit();
                                setOpen(false);
                            } else if (e.key === "Escape") {
                                setOpen(false);
                            } else if (e.key === "ArrowDown" && !open) {
                                e.preventDefault();
                                setOpen(true);
                            }
                        }}
                    />
                    <PopoverTrigger asChild>
                        <button type="button" tabIndex={-1} aria-label="Open time options" className="absolute right-2 flex h-6 w-6 items-center justify-center text-(--secondary-text-wMain) opacity-50 hover:opacity-100">
                            <ChevronDown className="h-4 w-4" />
                        </button>
                    </PopoverTrigger>
                </div>
            </PopoverAnchor>
            <PopoverContent align="start" sideOffset={4} className="max-h-[16rem] w-(--radix-popover-trigger-width) overflow-y-auto p-1" onOpenAutoFocus={e => e.preventDefault()}>
                <div className="flex flex-col">
                    {TIME_OF_DAY_OPTIONS.map(opt => {
                        const isSelected = parts[0] === opt.hour24 && parts[1] === opt.minute;
                        return (
                            <button key={opt.label} type="button" onClick={() => pick(opt.hour24, opt.minute)} className={`rounded-sm px-2 py-1.5 text-left text-sm hover:bg-(--secondary-w10) ${isSelected ? "font-medium" : ""}`}>
                                {opt.label}
                            </button>
                        );
                    })}
                </div>
            </PopoverContent>
        </Popover>
    );
};

export const SchedulePreviewBox: React.FC<{ description: string }> = ({ description }) => (
    <div className="rounded-md bg-(--secondary-w10) px-3 py-2">
        <p className="text-xs text-(--secondary-text-wMain)">Preview</p>
        <p className="mt-0.5 text-sm">{description}</p>
    </div>
);

export function ScheduleBuilder({ value, onChange }: { value: string; onChange: (cron: string) => void }) {
    const [rawCron, setRawCron] = useState(value);
    const [cronValidation, setCronValidation] = useState<{ valid: boolean; error?: string }>({ valid: true });

    const onChangeRef = useRef(onChange);
    onChangeRef.current = onChange;

    const initialConfig: ScheduleConfig = cronToSchedule(value) || {
        frequency: "daily",
        time: "09:00",
        ampm: "AM",
        weekDays: [],
        monthlyMode: "weekday",
        monthNth: "1",
        monthWeekday: 1,
        monthDay: 1,
    };

    const [config, setConfig] = useState<ScheduleConfig>(initialConfig);

    useEffect(() => {
        if (config.frequency === "custom") {
            setRawCron(value);
            return;
        }
        if (value === scheduleToCron(config)) return;
        const parsed = cronToSchedule(value);
        if (parsed) {
            setConfig(parsed);
            setRawCron(value);
        }
    }, [value]);

    useEffect(() => {
        if (config.frequency !== "custom") {
            const newCron = scheduleToCron(config);
            setRawCron(newCron);
            onChangeRef.current(newCron);
        }
    }, [config]);

    const handleRawCronChange = (newCron: string) => {
        setRawCron(newCron);
        const validation = validateCron(newCron);
        setCronValidation(validation);

        if (validation.valid) {
            onChangeRef.current(newCron);
        }
    };

    const updateConfig = (updates: Partial<ScheduleConfig>) => {
        if (config.frequency === "custom" && updates.frequency && updates.frequency !== "custom") {
            const parsedConfig = cronToSchedule(rawCron);
            if (parsedConfig) {
                setConfig({ ...parsedConfig, ...updates });
                return;
            }
        }

        if (config.frequency !== "custom" && updates.frequency === "custom") {
            const currentCron = scheduleToCron(config);
            setRawCron(currentCron);
            const validation = validateCron(currentCron);
            setCronValidation(validation);
        }

        setConfig({ ...config, ...updates });
    };

    if (config.frequency === "custom") {
        return (
            <div className="space-y-3">
                <div>
                    <label className="mb-2 block text-xs text-(--secondary-text-wMain)">Frequency</label>
                    <Select value={config.frequency} onValueChange={val => updateConfig({ frequency: val as FrequencyType })}>
                        <SelectTrigger className="min-w-[10rem]">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="daily">Daily</SelectItem>
                            <SelectItem value="weekly">Weekly</SelectItem>
                            <SelectItem value="monthly">Monthly</SelectItem>
                            <SelectItem value="custom">Custom (Cron Expression)</SelectItem>
                        </SelectContent>
                    </Select>
                </div>

                <div className="grid grid-cols-2 gap-4">
                    <div>
                        <label className="mb-2 block text-sm font-medium">Cron Expression</label>
                        <div className="relative">
                            <input type="text" className="w-full rounded-xs border px-3 py-2 pr-10 font-mono text-sm" value={rawCron} onChange={e => handleRawCronChange(e.target.value)} placeholder="0 9 * * *" />
                            {cronValidation.valid && (
                                <div className="absolute top-1/2 right-3 -translate-y-1/2">
                                    <CircleCheckBig className="h-4 w-4 text-(--success-wMain)" />
                                </div>
                            )}
                        </div>
                        {!cronValidation.valid && cronValidation.error ? (
                            <MessageBanner variant="error" message={cronValidation.error} className="mt-2" />
                        ) : (
                            <p className="mt-1 text-xs text-(--secondary-text-wMain)">
                                Format: <span className="font-mono">minute hour day month weekday</span>
                            </p>
                        )}
                    </div>
                </div>

                {cronValidation.valid && rawCron.trim() && <SchedulePreviewBox description={describeScheduleExpression("cron", rawCron)} />}

                <div className="space-y-2 rounded-lg bg-(--secondary-w10) p-3">
                    <p className="text-xs text-(--secondary-text-wMain)">Examples</p>
                    <div className="grid grid-cols-2 gap-x-6 gap-y-1 text-xs">
                        {[
                            ["0 9 * * *", "Every day at 9:00 AM"],
                            ["0 0 1 * *", "First day of the month at midnight"],
                            ["0 */6 * * *", "Every 6 hours"],
                            ["0 9 * * 1", "Every Monday at 9:00 AM"],
                        ].map(([cron, label]) => (
                            <div key={cron} className="flex items-center gap-4">
                                <span className="w-20 font-mono text-(--primary-wMain)">{cron}</span>
                                <span className="text-(--secondary-text-wMain)">{label}</span>
                            </div>
                        ))}
                    </div>
                    <p className="mt-2 text-xs text-(--secondary-text-wMain)">
                        Use <span className="font-mono">*</span> for "any", <span className="font-mono">,</span> for lists, <span className="font-mono">-</span> for ranges, <span className="font-mono">/</span> for intervals
                    </p>
                </div>
            </div>
        );
    }

    const time24 = (() => {
        const parts = config.time.split(":");
        const h12 = Number(parts[0]);
        const m = Number(parts[1]);
        if (isNaN(h12) || isNaN(m)) return "09:00";
        let h24 = h12;
        if (config.ampm === "PM") h24 = h12 === 12 ? 12 : h12 + 12;
        else if (config.ampm === "AM") h24 = h12 === 12 ? 0 : h12;
        return `${String(h24).padStart(2, "0")}:${String(m).padStart(2, "0")}`;
    })();

    return (
        <div className="space-y-4">
            <div className="flex flex-wrap items-end gap-3">
                <div>
                    <label className="mb-2 block text-xs text-(--secondary-text-wMain)">Frequency</label>
                    <Select value={config.frequency} onValueChange={val => updateConfig({ frequency: val as FrequencyType })}>
                        <SelectTrigger className="min-w-[10rem]">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="daily">Daily</SelectItem>
                            <SelectItem value="weekly">Weekly</SelectItem>
                            <SelectItem value="monthly">Monthly</SelectItem>
                            <SelectItem value="custom">Custom (Cron Expression)</SelectItem>
                        </SelectContent>
                    </Select>
                </div>

                <div>
                    <label className="mb-2 block text-xs text-(--secondary-text-wMain)">Time</label>
                    <TimeOfDayPicker
                        value={time24}
                        onChange={val => {
                            const [h24, m] = val.split(":").map(Number);
                            const ampm: "AM" | "PM" = h24 >= 12 ? "PM" : "AM";
                            const h12 = h24 % 12 || 12;
                            updateConfig({
                                time: `${String(h12).padStart(2, "0")}:${String(m).padStart(2, "0")}`,
                                ampm,
                            });
                        }}
                    />
                </div>

                {config.frequency === "monthly" && (
                    <>
                        <div>
                            <label className="mb-2 block text-xs text-(--secondary-text-wMain)">On</label>
                            <Select value={config.monthlyMode} onValueChange={val => updateConfig({ monthlyMode: val as MonthlyMode })}>
                                <SelectTrigger className="min-w-[8rem]">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="weekday">Weekday</SelectItem>
                                    <SelectItem value="date">Date</SelectItem>
                                </SelectContent>
                            </Select>
                        </div>

                        {config.monthlyMode === "weekday" ? (
                            <>
                                <div>
                                    <label className="mb-2 block text-xs text-(--secondary-text-wMain)">&nbsp;</label>
                                    <Select value={config.monthNth} onValueChange={val => updateConfig({ monthNth: val as MonthNth })}>
                                        <SelectTrigger className="min-w-[7rem]">
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent>
                                            {(Object.keys(MONTH_NTH_LABELS) as MonthNth[]).map(key => (
                                                <SelectItem key={key} value={key}>
                                                    {MONTH_NTH_LABELS[key]}
                                                </SelectItem>
                                            ))}
                                        </SelectContent>
                                    </Select>
                                </div>

                                <div>
                                    <label className="mb-2 block text-xs text-(--secondary-text-wMain)">&nbsp;</label>
                                    <Select value={String(config.monthWeekday)} onValueChange={val => updateConfig({ monthWeekday: parseInt(val) })}>
                                        <SelectTrigger className="min-w-[8rem]">
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent>
                                            {DAY_LABELS.map((dayName, idx) => (
                                                <SelectItem key={idx} value={String(idx)}>
                                                    {dayName}
                                                </SelectItem>
                                            ))}
                                        </SelectContent>
                                    </Select>
                                </div>
                            </>
                        ) : (
                            <div>
                                <label className="mb-2 block text-xs text-(--secondary-text-wMain)">Day of Month</label>
                                <Select value={String(config.monthDay)} onValueChange={val => updateConfig({ monthDay: parseInt(val) })}>
                                    <SelectTrigger className="min-w-[7rem]">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {Array.from({ length: 31 }, (_, i) => i + 1).map(day => (
                                            <SelectItem key={day} value={String(day)}>
                                                {day}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                        )}
                    </>
                )}

                {config.frequency === "weekly" && (
                    <div className="min-w-[18rem] flex-1">
                        <label className="mb-2 block text-xs text-(--secondary-text-wMain)">Days of the Week</label>
                        <DaysOfWeekPicker selected={config.weekDays} onChange={days => updateConfig({ weekDays: days })} />
                    </div>
                )}
            </div>

            {config.frequency !== "daily" && <SchedulePreviewBox description={getScheduleDescription(config)} />}
        </div>
    );
}
