// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const DAY_LABELS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

export type FrequencyType = "daily" | "weekly" | "monthly" | "custom";

export type MonthNth = "1" | "2" | "3" | "4" | "L";

export type MonthlyMode = "weekday" | "date";

export const MONTH_NTH_LABELS: Record<MonthNth, string> = {
    "1": "First",
    "2": "Second",
    "3": "Third",
    "4": "Fourth",
    L: "Last",
};

export const MONTHLY_WEEKDAY_PATTERN = /^([0-6])(?:#([1-5])|L)$/i;

const NTH_OCCURRENCE_LABELS = ["First", "Second", "Third", "Fourth", "Fifth"];

export function describeMonthlyWeekday(dayOfWeek: string): string | null {
    const match = MONTHLY_WEEKDAY_PATTERN.exec(dayOfWeek);
    if (!match) return null;
    const occurrence = match[2] ? NTH_OCCURRENCE_LABELS[Number(match[2]) - 1] : "Last";
    return `${occurrence} ${DAY_LABELS[Number(match[1])]} of every month`;
}

export interface ScheduleConfig {
    frequency: FrequencyType;
    time: string;
    ampm: "AM" | "PM";
    weekDays: number[];
    monthlyMode: MonthlyMode;
    monthNth: MonthNth;
    monthWeekday: number;
    monthDay: number;
}

export function scheduleToCron(config: ScheduleConfig): string {
    const parts = config.time.split(":");
    const hours12 = Number(parts[0]);
    const minutes = Number(parts[1]);

    if (isNaN(hours12) || isNaN(minutes)) {
        return "0 9 * * *";
    }

    let hour = hours12;

    if (config.ampm === "PM") {
        hour = hours12 === 12 ? 12 : hours12 + 12;
    } else if (config.ampm === "AM") {
        hour = hours12 === 12 ? 0 : hours12;
    }

    switch (config.frequency) {
        case "daily":
            return `${minutes} ${hour} * * *`;

        case "weekly": {
            if (config.weekDays.length === 0) return `${minutes} ${hour} * * *`;
            const days = [...config.weekDays].sort((a, b) => a - b).join(",");
            return `${minutes} ${hour} * * ${days}`;
        }

        case "monthly": {
            if (config.monthlyMode === "date") {
                const day = Math.max(1, Math.min(31, Math.floor(config.monthDay) || 1));
                return `${minutes} ${hour} ${day} * *`;
            }
            const wd = config.monthWeekday;
            if (config.monthNth === "L") {
                return `${minutes} ${hour} * * ${wd}L`;
            }
            return `${minutes} ${hour} * * ${wd}#${config.monthNth}`;
        }

        default:
            return `${minutes} ${hour} * * *`;
    }
}

export function normalizeCron(cron: string): string {
    return cron
        .trim()
        .split(/\s+/)
        .map(field => {
            if (!field.includes(",")) return field;
            const nums = field.split(",").map(Number);
            if (nums.some(n => isNaN(n))) return field;
            return [...new Set(nums)].sort((a, b) => a - b).join(",");
        })
        .join(" ");
}

export function cronToSchedule(cron: string): ScheduleConfig | null {
    const parts = cron.trim().split(/\s+/);
    if (parts.length !== 5) return null;

    const [minute, hour, dayOfMonth, , dayOfWeek] = parts;

    let hours24 = 9;
    let minutes = 0;

    if (hour !== "*" && !hour.includes("/")) {
        hours24 = parseInt(hour);
    }
    if (minute !== "*" && !minute.includes("/")) {
        minutes = parseInt(minute);
    }

    let ampm: "AM" | "PM" = "AM";
    let hours12 = hours24;
    if (hours24 >= 12) {
        ampm = "PM";
        if (hours24 > 12) hours12 = hours24 - 12;
    } else if (hours24 === 0) {
        hours12 = 12;
    }

    const time = `${hours12.toString().padStart(2, "0")}:${minutes.toString().padStart(2, "0")}`;

    const customFallback: ScheduleConfig = {
        frequency: "custom",
        time,
        ampm,
        weekDays: [],
        monthlyMode: "weekday",
        monthNth: "1",
        monthWeekday: 1,
        monthDay: 1,
    };

    let candidate: ScheduleConfig;

    if (minute.includes("/") || hour.includes("/")) {
        return customFallback;
    } else if (dayOfMonth === "*" && /^\d#[1-4]$/.test(dayOfWeek)) {
        const [wdStr, nStr] = dayOfWeek.split("#");
        const wd = parseInt(wdStr);
        if (wd < 0 || wd > 6) return customFallback;
        candidate = { frequency: "monthly", time, ampm, weekDays: [], monthlyMode: "weekday", monthNth: nStr as MonthNth, monthWeekday: wd, monthDay: 1 };
    } else if (dayOfMonth === "*" && /^\dL$/i.test(dayOfWeek)) {
        const wd = parseInt(dayOfWeek[0]);
        if (wd < 0 || wd > 6) return customFallback;
        candidate = { frequency: "monthly", time, ampm, weekDays: [], monthlyMode: "weekday", monthNth: "L", monthWeekday: wd, monthDay: 1 };
    } else if (dayOfWeek === "*" && /^\d+$/.test(dayOfMonth)) {
        const day = parseInt(dayOfMonth);
        if (day < 1 || day > 31) return customFallback;
        candidate = { frequency: "monthly", time, ampm, weekDays: [], monthlyMode: "date", monthNth: "1", monthWeekday: 1, monthDay: day };
    } else if (dayOfWeek !== "*" && /^\d+(,\d+)*$/.test(dayOfWeek)) {
        const days = dayOfWeek.split(",").map(d => parseInt(d));
        candidate = { frequency: "weekly", time, ampm, weekDays: days, monthlyMode: "weekday", monthNth: "1", monthWeekday: 1, monthDay: 1 };
    } else if (dayOfMonth === "*" && dayOfWeek === "*") {
        candidate = { frequency: "daily", time, ampm, weekDays: [], monthlyMode: "weekday", monthNth: "1", monthWeekday: 1, monthDay: 1 };
    } else {
        return customFallback;
    }

    if (normalizeCron(scheduleToCron(candidate)) !== normalizeCron(cron)) {
        return customFallback;
    }
    return candidate;
}

export function getScheduleDescription(config: ScheduleConfig): string {
    const timeStr = `${config.time} ${config.ampm}`;

    switch (config.frequency) {
        case "daily":
            return `Every day at ${timeStr}`;

        case "weekly": {
            if (config.weekDays.length === 0) return `Every day at ${timeStr}`;
            const days = [...config.weekDays]
                .sort((a, b) => a - b)
                .map(d => DAY_LABELS[d])
                .join(", ");
            return `Every ${days} at ${timeStr}`;
        }

        case "monthly": {
            if (config.monthlyMode === "date") {
                const day = Math.max(1, Math.min(31, Math.floor(config.monthDay) || 1));
                const mod100 = day % 100;
                const suffix = mod100 >= 11 && mod100 <= 13 ? "th" : day % 10 === 1 ? "st" : day % 10 === 2 ? "nd" : day % 10 === 3 ? "rd" : "th";
                return `Monthly on the ${day}${suffix} at ${timeStr}`;
            }
            const dayName = DAY_LABELS[config.monthWeekday] || "Monday";
            const ordinal = MONTH_NTH_LABELS[config.monthNth].toLowerCase();
            return `${ordinal === "last" ? "Last" : ordinal[0].toUpperCase() + ordinal.slice(1)} ${dayName} of every month at ${timeStr}`;
        }

        default:
            return "Custom schedule";
    }
}

function isValidCronField(field: string, min: number, max: number): boolean {
    if (!field) return false;
    if (field.includes(",")) {
        return field.split(",").every(element => isValidCronElement(element, min, max));
    }
    return isValidCronElement(field, min, max);
}

function isValidCronElement(element: string, min: number, max: number): boolean {
    if (!element) return false;

    if (element.includes("/")) {
        const [base, stepStr] = element.split("/");
        const step = parseInt(stepStr);
        if (isNaN(step) || String(step) !== stepStr || step <= 0 || step > max) return false;
        if (base === "*") return true;
        if (base.includes("-")) return isValidCronRange(base, min, max);
        return false;
    }

    if (element.includes("-")) return isValidCronRange(element, min, max);
    if (element === "*") return true;

    const value = parseInt(element);
    return !isNaN(value) && String(value) === element && value >= min && value <= max;
}

function isValidCronRange(range: string, min: number, max: number): boolean {
    const parts = range.split("-");
    if (parts.length !== 2) return false;
    const [start, end] = parts.map(Number);
    if (isNaN(start) || isNaN(end)) return false;
    if (String(start) !== parts[0] || String(end) !== parts[1]) return false;
    return start >= min && end <= max && start <= end;
}

export function validateCron(cron: string): { valid: boolean; error?: string } {
    const trimmed = cron.trim();
    if (!trimmed) {
        return { valid: false, error: "Cron expression cannot be empty" };
    }

    const parts = trimmed.split(/\s+/);
    if (parts.length !== 5) {
        return { valid: false, error: "Cron expression must have exactly 5 fields (minute hour day month weekday)" };
    }

    const [minute, hour, dayOfMonth, month, dayOfWeek] = parts;

    if (!isValidCronField(minute, 0, 59)) {
        return { valid: false, error: "Invalid minute field (must be 0-59 or * or */n)" };
    }
    if (!isValidCronField(hour, 0, 23)) {
        return { valid: false, error: "Invalid hour field (must be 0-23 or * or */n)" };
    }
    if (!isValidCronField(dayOfMonth, 1, 31)) {
        return { valid: false, error: "Invalid day of month field (must be 1-31 or * or */n)" };
    }
    if (!isValidCronField(month, 1, 12)) {
        return { valid: false, error: "Invalid month field (must be 1-12 or * or */n)" };
    }
    if (MONTHLY_WEEKDAY_PATTERN.test(dayOfWeek)) {
        if (dayOfMonth !== "*") {
            return { valid: false, error: "Day of month must be * when day of week uses # or L" };
        }
    } else if (!isValidCronField(dayOfWeek, 0, 6)) {
        return { valid: false, error: "Invalid day of week field (must be 0-6, * or */n, D#N for the Nth weekday, or DL for the last)" };
    }

    return { valid: true };
}
