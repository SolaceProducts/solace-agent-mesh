// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { CalendarDays, FlaskConical, FolderOpen, Hammer, MessageCircle } from "lucide-react";

import type { Session } from "@/types";
import { cn, isEphemeralTestSession } from "@/utils";

interface SessionIconProps {
    session: Session;
    className?: string;
}

export function SessionIcon({ session, className }: SessionIconProps) {
    const iconClass = cn("h-3.5 w-3.5 shrink-0", className);

    if (isEphemeralTestSession(session)) {
        return <FlaskConical className={iconClass} />;
    }
    if (session.agentId === "Builder") {
        return <Hammer className={iconClass} />;
    }
    if (session.projectId) {
        return <FolderOpen className={iconClass} />;
    }
    if (session.source === "scheduler") {
        return <CalendarDays className={iconClass} />;
    }
    return <MessageCircle className={iconClass} />;
}
