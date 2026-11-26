// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ScheduledTask } from "@/types/scheduled-tasks";

import { describeMonthlyWeekday } from "./schedule-cron";

const DAY_NAMES_SHORT = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const DAY_NAMES_LONG = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

const formatTime12h = (hour24: number, minute: number): string => {
    const ampm = hour24 >= 12 ? "PM" : "AM";
    const h = hour24 % 12 || 12;
    return `${h}:${String(minute).padStart(2, "0")} ${ampm}`;
};

const ordinal = (n: number): string => {
    const mod100 = n % 100;
    if (mod100 >= 11 && mod100 <= 13) return `${n}th`;
    switch (n % 10) {
        case 1:
            return `${n}st`;
        case 2:
            return `${n}nd`;
        case 3:
            return `${n}rd`;
        default:
            return `${n}th`;
    }
};

const describeCron = (cron: string): string | null => {
    const parts = cron.trim().split(/\s+/);
    if (parts.length !== 5) return null;
    const [minute, hour, dayOfMonth, month, dayOfWeek] = parts;

    if (month !== "*") return null;
    if (minute.includes("-") || minute.includes(",")) return null;

    const everyDay = dayOfMonth === "*" && dayOfWeek === "*";
    if (minute.startsWith("*/") && hour === "*" && everyDay) return describeMinuteStep(minute);
    if (hour.startsWith("*/") && everyDay) return describeHourStep(minute, hour);

    const timeStr = describeClockTime(minute, hour);
    return timeStr === null ? null : describeDays(dayOfMonth, dayOfWeek, timeStr);
};

const describeMinuteStep = (minute: string): string | null => {
    const interval = Number.parseInt(minute.substring(2), 10);
    if (Number.isNaN(interval) || interval <= 0) return null;
    return interval === 1 ? "Every minute" : `Every ${interval} minutes`;
};

const describeHourStep = (minute: string, hour: string): string | null => {
    const interval = Number.parseInt(hour.substring(2), 10);
    if (Number.isNaN(interval) || interval <= 0) return null;
    if (minute === "0") return interval === 1 ? "Every hour" : `Every ${interval} hours`;
    const m = Number.parseInt(minute, 10);
    if (Number.isNaN(m)) return null;
    const offset = `:${String(m).padStart(2, "0")}`;
    return interval === 1 ? `Every hour at ${offset}` : `Every ${interval} hours at ${offset}`;
};

const describeClockTime = (minute: string, hour: string): string | null => {
    const m = Number.parseInt(minute, 10);
    const h = Number.parseInt(hour, 10);
    if (Number.isNaN(m) || Number.isNaN(h) || h < 0 || h > 23 || m < 0 || m > 59) return null;
    return formatTime12h(h, m);
};

const describeDays = (dayOfMonth: string, dayOfWeek: string, timeStr: string): string | null => {
    if (dayOfMonth !== "*") return dayOfWeek === "*" ? describeDayOfMonth(dayOfMonth, timeStr) : null;
    if (dayOfWeek === "*") return `Every day at ${timeStr}`;
    return monthlyWeekdayAt(dayOfMonth, dayOfWeek, timeStr) ?? describeWeekdays(dayOfWeek, timeStr);
};

const monthlyWeekdayAt = (dayOfMonth: string, dayOfWeek: string, time: string): string | null => {
    const monthlyWeekday = dayOfMonth === "*" ? describeMonthlyWeekday(dayOfWeek) : null;
    return monthlyWeekday ? `${monthlyWeekday} at ${time}` : null;
};

const describeWeekdays = (dayOfWeek: string, timeStr: string): string | null => {
    const days = dayOfWeek.split(",").map(d => Number.parseInt(d, 10));
    if (days.some(Number.isNaN) || days.some(d => d < 0 || d > 6)) return null;
    const sorted = [...new Set(days)].sort((a, b) => a - b);
    if (sorted.length === 7) return `Every day at ${timeStr}`;
    if (sorted.length === 5 && sorted.join(",") === "1,2,3,4,5") return `Weekdays at ${timeStr}`;
    if (sorted.length === 2 && sorted.join(",") === "0,6") return `Weekends at ${timeStr}`;
    if (sorted.length === 1) return `Every ${DAY_NAMES_LONG[sorted[0]]} at ${timeStr}`;
    return `${sorted.map(d => DAY_NAMES_SHORT[d]).join(", ")} at ${timeStr}`;
};

const describeDayOfMonth = (dayOfMonth: string, timeStr: string): string | null => {
    const day = Number.parseInt(dayOfMonth, 10);
    if (Number.isNaN(day) || day < 1 || day > 31) return null;
    return `Monthly on the ${ordinal(day)} at ${timeStr}`;
};

export const describeScheduleExpression = (type: string, expression: string): string => {
    if (!expression) return "Not set";

    if (type === "cron") {
        return describeCron(expression) ?? expression;
    }

    if (type === "interval") {
        const match = /^(\d+)([smhd])$/i.exec(expression.trim());
        if (!match) return expression;
        const n = parseInt(match[1], 10);
        const unitMap: Record<string, [string, string]> = {
            s: ["second", "seconds"],
            m: ["minute", "minutes"],
            h: ["hour", "hours"],
            d: ["day", "days"],
        };
        const [singular, plural] = unitMap[match[2].toLowerCase()];
        return n === 1 ? `Every ${singular}` : `Every ${n} ${plural}`;
    }

    if (type === "one_time") {
        const date = new Date(expression);
        if (isNaN(date.getTime())) return expression;
        return date.toLocaleString(undefined, {
            weekday: "short",
            year: "numeric",
            month: "short",
            day: "numeric",
            hour: "numeric",
            minute: "2-digit",
            hour12: true,
        });
    }

    return expression;
};

const formatCronSchedule = (cron: string): string => {
    const parts = cron.trim().split(/\s+/);
    if (parts.length !== 5) return `Cron: ${cron}`;
    const [minute, hour, dayOfMonth, , dayOfWeek] = parts;

    if (hour.includes("/")) return `Every ${hour.split("/")[1]} hours`;

    const time = `${hour.padStart(2, "0")}:${minute.padStart(2, "0")}`;
    const monthlyWeekday = monthlyWeekdayAt(dayOfMonth, dayOfWeek, time);
    if (monthlyWeekday) return monthlyWeekday;
    if (dayOfWeek !== "*") return `${formatShortDayList(dayOfWeek)} at ${time}`;
    if (dayOfMonth !== "*") return `Monthly on day ${dayOfMonth} at ${time}`;
    if (hour !== "*" && minute !== "*") return `Daily at ${time}`;
    return `Cron: ${cron}`;
};

const formatShortDayList = (dayOfWeek: string): string =>
    dayOfWeek
        .split(",")
        .map(d => DAY_NAMES_SHORT[Number.parseInt(d)])
        .join(", ");

export const formatSchedule = (task: { scheduleType: ScheduledTask["scheduleType"]; scheduleExpression: string }): string => {
    if (task.scheduleType === "cron") {
        return formatCronSchedule(task.scheduleExpression);
    } else if (task.scheduleType === "interval") {
        return `Every ${task.scheduleExpression}`;
    } else {
        try {
            const date = new Date(task.scheduleExpression);
            const formatted = date.toLocaleString("en-US", {
                weekday: "short",
                year: "numeric",
                month: "short",
                day: "numeric",
                hour: "numeric",
                minute: "2-digit",
                hour12: true,
            });
            return `One time: ${formatted}`;
        } catch {
            return `One time: ${task.scheduleExpression}`;
        }
    }
};
