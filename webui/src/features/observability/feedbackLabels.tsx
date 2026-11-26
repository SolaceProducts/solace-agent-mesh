// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Bot, Monitor, ThumbsDown, ThumbsUp, Workflow } from "lucide-react";
import type React from "react";

import type { CrossUserFeedback } from "@/api/feedback";

import { Badge } from "@/ui/badge";

import { GoogleChatIcon, SlackIcon, TeamsIcon, WhatsAppIcon } from "@/features/entrypoints/GatewayTypeChip";

export const SUBJECT_TYPE_ICONS: Partial<Record<CrossUserFeedback["subjectType"], typeof Bot>> = {
    agent: Bot,
    workflow: Workflow,
};

export function subjectLabel(row: Pick<CrossUserFeedback, "subjectType" | "subjectName" | "subjectId">): string {
    if (row.subjectType === "unknown") return "Unknown";
    return row.subjectName || row.subjectId;
}

export function formatFeedbackTime(iso: string): string {
    return new Date(iso).toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
}

export function formatFeedbackTimeWithYear(iso: string): string {
    return new Date(iso).toLocaleString(undefined, { year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
}

const RATING_BADGE = "[&>svg]:size-3.5";

export function RatingBadge({ rating }: { rating: CrossUserFeedback["rating"] }) {
    return rating === "up" ? (
        <Badge type="success" className={RATING_BADGE} aria-label="Positive">
            <ThumbsUp />
        </Badge>
    ) : (
        <Badge type="error" className={RATING_BADGE} aria-label="Negative">
            <ThumbsDown />
        </Badge>
    );
}

type EntrypointIcon = React.ComponentType<{ size?: string }>;

const WebUIIcon: EntrypointIcon = ({ size = "16" }) => <Monitor size={Number(size)} />;

const ENTRYPOINTS: Record<string, { label: string; Icon: EntrypointIcon }> = {
    webui: { label: "Web UI", Icon: WebUIIcon },
    slack: { label: "Slack", Icon: SlackIcon },
    teams_bot: { label: "Microsoft Teams", Icon: TeamsIcon },
    whatsapp: { label: "WhatsApp", Icon: WhatsAppIcon },
    google_chat: { label: "Google Chat", Icon: GoogleChatIcon },
};

export function entrypointDisplay(type: string): { label: string; Icon: EntrypointIcon | null } {
    return ENTRYPOINTS[type] ?? { label: "Unknown", Icon: null };
}
