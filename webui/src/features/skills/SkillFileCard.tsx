// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import type { LucideIcon } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

interface SkillFileCardProps {
    icon: LucideIcon;
    title: string;
    subtitle?: string;
    onClick?: () => void;
    isActive?: boolean;
}

export const SkillFileCard: React.FC<SkillFileCardProps> = ({ icon: Icon, title, subtitle, onClick, isActive }) => {
    const body = (
        <>
            <div className="flex-shrink-0">
                <div className="mr-4 flex h-12 w-12 items-center justify-center rounded-full bg-(--secondary-w10)">
                    <Icon className="size-5" />
                </div>
            </div>
            <div className="flex min-w-0 flex-1 flex-col">
                <div className="truncate font-medium" title={title}>
                    {title}
                </div>
                {subtitle && <div className="text-small mt-1 truncate text-(--secondary-text-wMain)">{subtitle}</div>}
            </div>
        </>
    );

    if (!onClick) {
        return <div className="flex items-center gap-2 rounded-sm border p-4">{body}</div>;
    }

    return (
        <button type="button" onClick={onClick} className={cn("flex w-full items-center gap-2 rounded-sm border p-4 text-left transition-colors hover:bg-(--secondary-w10)", isActive && "border-(--brand-wMain)")}>
            {body}
        </button>
    );
};
