// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";
import { CalendarIcon, ChevronLeftIcon, ChevronRightIcon, X } from "lucide-react";

import { cn } from "@/utils";

import { Button } from "./button";
import { FIELD_TRIGGER } from "./field-trigger";
import { Popover, PopoverContent, PopoverTrigger } from "./popover";

const DAYS_OF_WEEK = [
    { letter: "S", label: "Sunday" },
    { letter: "M", label: "Monday" },
    { letter: "T", label: "Tuesday" },
    { letter: "W", label: "Wednesday" },
    { letter: "T", label: "Thursday" },
    { letter: "F", label: "Friday" },
    { letter: "S", label: "Saturday" },
];
const MONTHS = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];

function getDaysInMonth(year: number, month: number): number {
    return new Date(year, month + 1, 0).getDate();
}

function getFirstDayOfMonth(year: number, month: number): number {
    return new Date(year, month, 1).getDay();
}

function formatDate(date: Date): string {
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, "0");
    const day = String(date.getDate()).padStart(2, "0");
    return `${year}-${month}-${day}`;
}

function parseDate(value: string): Date | null {
    if (!value) return null;
    const [year, month, day] = value.split("-").map(Number);
    if (!year || !month || !day) return null;
    const date = new Date(year, month - 1, day);
    return isNaN(date.getTime()) ? null : date;
}

interface DatePickerProps {
    value: string;
    onChange: (value: string) => void;
    min?: string;
    max?: string;
    ariaLabel?: string;
    placeholder?: string;
    className?: string;
    invalid?: boolean;
}

function DatePicker({ value, onChange, min, max, ariaLabel, placeholder = "Pick a date", className, invalid }: Readonly<DatePickerProps>) {
    const [open, setOpen] = useState(false);
    const selectedDate = parseDate(value);
    const minDate = parseDate(min ?? "");
    const maxDate = parseDate(max ?? "");

    const today = useMemo(() => {
        const d = new Date();
        d.setHours(0, 0, 0, 0);
        return d;
    }, []);

    const [viewYear, setViewYear] = useState(() => selectedDate?.getFullYear() ?? today.getFullYear());
    const [viewMonth, setViewMonth] = useState(() => selectedDate?.getMonth() ?? today.getMonth());

    const daysInMonth = getDaysInMonth(viewYear, viewMonth);
    const firstDay = getFirstDayOfMonth(viewYear, viewMonth);

    const prevMonth = () => {
        if (viewMonth === 0) {
            setViewMonth(11);
            setViewYear(y => y - 1);
        } else {
            setViewMonth(m => m - 1);
        }
    };

    const nextMonth = () => {
        if (viewMonth === 11) {
            setViewMonth(0);
            setViewYear(y => y + 1);
        } else {
            setViewMonth(m => m + 1);
        }
    };

    const isDisabled = (day: number) => {
        const date = new Date(viewYear, viewMonth, day);
        date.setHours(0, 0, 0, 0);
        if (minDate && date < minDate) return true;
        return Boolean(maxDate && date > maxDate);
    };

    const isSelected = (day: number) => {
        if (!selectedDate) return false;
        return selectedDate.getFullYear() === viewYear && selectedDate.getMonth() === viewMonth && selectedDate.getDate() === day;
    };

    const isToday = (day: number) => {
        return today.getFullYear() === viewYear && today.getMonth() === viewMonth && today.getDate() === day;
    };

    const handleSelect = (day: number) => {
        const date = new Date(viewYear, viewMonth, day);
        onChange(formatDate(date));
        setOpen(false);
    };

    const displayValue = selectedDate ? formatDate(selectedDate) : null;

    return (
        <Popover open={open} onOpenChange={setOpen}>
            <div className={cn("relative block w-full", className)}>
                <PopoverTrigger asChild>
                    <Button aria-label={ariaLabel} variant="outline" className={cn(FIELD_TRIGGER, "w-full pr-9 pl-3", value ? "text-(--primary-text-wMain)" : "text-(--secondary-text-wMain)", invalid && "border-(--error-w100)")}>
                        <span className="truncate">{displayValue ?? placeholder}</span>
                        <CalendarIcon className="h-4 w-4 flex-shrink-0 text-(--secondary-text-wMain)" />
                    </Button>
                </PopoverTrigger>
                {value && (
                    <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        aria-label="Clear date"
                        onClick={e => {
                            e.stopPropagation();
                            onChange("");
                        }}
                        className="absolute top-1/2 right-7 h-5 w-5 -translate-y-1/2 p-0 hover:bg-(--secondary-w20)"
                    >
                        <X className="h-3.5 w-3.5 text-(--secondary-text-wMain)" />
                    </Button>
                )}
            </div>
            <PopoverContent className="w-auto p-3" align="start">
                <div className="mb-2 flex items-center justify-between">
                    <span className="text-sm font-medium">
                        {MONTHS[viewMonth]} {viewYear}
                    </span>
                    <div className="flex items-center gap-1">
                        <button type="button" onClick={prevMonth} className="rounded p-1 hover:bg-(--primary-w10)">
                            <ChevronLeftIcon className="h-4 w-4" />
                        </button>
                        <button type="button" onClick={nextMonth} className="rounded p-1 hover:bg-(--primary-w10)">
                            <ChevronRightIcon className="h-4 w-4" />
                        </button>
                    </div>
                </div>

                <div className="grid grid-cols-7 gap-0">
                    {DAYS_OF_WEEK.map(({ letter, label }) => (
                        <div key={label} aria-label={label} className="py-1 text-center text-xs font-medium text-(--secondary-text-wMain)">
                            <span aria-hidden="true">{letter}</span>
                        </div>
                    ))}

                    {Array.from({ length: firstDay }, (_, i) => (
                        <div key={`empty-${i}`} />
                    ))}

                    {Array.from({ length: daysInMonth }, (_, i) => {
                        const day = i + 1;
                        const disabled = isDisabled(day);
                        const selected = isSelected(day);
                        const todayCell = isToday(day);

                        return (
                            <button
                                key={day}
                                type="button"
                                disabled={disabled}
                                onClick={() => handleSelect(day)}
                                className={cn(
                                    "m-0.5 h-8 w-8 rounded-full text-sm transition-colors",
                                    disabled && "cursor-not-allowed opacity-30",
                                    !disabled && !selected && "hover:bg-(--primary-w10)",
                                    selected && "bg-(--primary-wMain) text-(--primary-text-w10)",
                                    todayCell && !selected && "font-bold"
                                )}
                            >
                                {day}
                            </button>
                        );
                    })}
                </div>
            </PopoverContent>
        </Popover>
    );
}

export { DatePicker };
