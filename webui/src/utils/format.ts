// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const formatBytes = (bytes: number, decimals = 2): string => {
    if (bytes === 0) return "0 Bytes";
    if (bytes < 0 || !Number.isFinite(bytes)) return "Invalid size";
    const k = 1024;
    const dm = decimals < 0 ? 0 : decimals;
    const sizes = ["Bytes", "KB", "MB", "GB", "TB", "PB", "EB", "ZB", "YB"];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + " " + sizes[i];
};

export const formatRelativeTime = (dateString: string): string => {
    if (!dateString) return "N/A";
    try {
        const date = new Date(dateString);
        if (isNaN(date.getTime())) return "N/A";

        const now = new Date();
        const diffInSeconds = Math.floor((now.getTime() - date.getTime()) / 1000);
        const diffInMinutes = Math.floor(diffInSeconds / 60);
        const diffInHours = Math.floor(diffInMinutes / 60);
        const diffInDays = Math.floor(diffInHours / 24);

        const diffInWeeks = Math.floor(diffInDays / 7);
        const diffInMonths = Math.floor(diffInDays / 30);

        if (diffInSeconds < 60) return `${diffInSeconds}s ago`;
        if (diffInMinutes < 60) return `${diffInMinutes}m ago`;
        if (diffInHours < 24) return `${diffInHours}h ago`;
        if (diffInDays === 1) return `Yesterday`;
        if (diffInDays < 7) return `${diffInDays}d ago`;
        if (diffInWeeks < 5) return `${diffInWeeks} week${diffInWeeks === 1 ? "" : "s"} ago`;
        if (diffInMonths < 12) return `${diffInMonths} month${diffInMonths === 1 ? "" : "s"} ago`;
        const diffInYears = Math.floor(diffInDays / 365);
        return `${diffInYears} year${diffInYears === 1 ? "" : "s"} ago`;
    } catch (e) {
        console.error("Error formatting date:", e);
        return "Invalid date";
    }
};

export const formatEpochTimestamp = (timestamp: string | number): string => {
    if (typeof timestamp === "string") {
        return new Date(timestamp).toLocaleString();
    }
    const ts = timestamp < 10000000000 ? timestamp * 1000 : timestamp;
    const date = new Date(ts);
    return date.toLocaleString();
};

const pad = (n: number) => String(n).padStart(2, "0");

export const formatEpochTimestampShort = (timestamp: string | number): string => {
    const d = typeof timestamp === "string" ? new Date(timestamp) : new Date(timestamp < 10_000_000_000 ? timestamp * 1000 : timestamp);
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
};

export const formatDuration = (ms: number): string => {
    if (ms < 1000) return `${ms}ms`;
    if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
    if (ms < 3600000) return `${(ms / 60000).toFixed(1)}m`;
    return `${(ms / 3600000).toFixed(1)}h`;
};

const toDate = (ts: string | number): Date => {
    if (typeof ts === "string") return new Date(ts);
    const ms = ts < 10_000_000_000 ? ts * 1000 : ts;
    return new Date(ms);
};

export const formatDateYMD = (ts: string | number): string => {
    const d = toDate(ts);
    return `${d.getFullYear()}/${pad(d.getMonth() + 1)}/${pad(d.getDate())}`;
};

export const formatTimestamp = (isoString?: string | null, format: "datetime" | "date" | "time" = "datetime"): string => {
    if (!isoString) return "N/A";
    try {
        const date = new Date(isoString);
        if (isNaN(date.getTime())) return "N/A";
        switch (format) {
            case "date":
                return date.toLocaleDateString();
            case "time":
                return date.toLocaleTimeString();
            default:
                return date.toLocaleString();
        }
    } catch {
        return "N/A";
    }
};

export const formatActor = (actor?: string) => (actor?.toLowerCase() === "system" ? "System" : actor);

export const formatTokenCount = (tokens: number): string => {
    if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(1)}M`;
    if (tokens >= 1_000) return `${(tokens / 1_000).toFixed(1)}K`;
    return tokens.toString();
};
