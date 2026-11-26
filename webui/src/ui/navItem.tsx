// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cn } from "@/utils";

import { LifecycleBadge } from "./lifecycleBadge";

export interface NavItemProps {
    id: string;
    label: string;
    isActive: boolean;
    onClick: () => void;
    badge?: string;
}

export const NavItem = ({ label, isActive, onClick, badge }: NavItemProps) => (
    <button role="tab" aria-selected={isActive} onClick={onClick} className={cn("h-11 cursor-pointer px-4 font-medium focus-visible:-outline-offset-1", isActive ? "font-semibold shadow-[inset_0_-2px_0_var(--brand-wMain)]" : "")}>
        <div className="flex h-full items-center gap-2">
            {label}
            {badge && <LifecycleBadge variant="transparent">{badge}</LifecycleBadge>}
        </div>
    </button>
);
