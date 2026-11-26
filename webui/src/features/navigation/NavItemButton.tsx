// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { ChevronDown, ChevronUp } from "lucide-react";
import { Link } from "react-router-dom";

import { cn } from "@/utils";

import { iconStyles, iconWrapperStyles, navButtonStyles, navTextStyles } from "./navigationStyles";
import type { NavItem } from "./types";

export interface NavItemButtonProps {
    item: NavItem;
    isActive: boolean;
    onClick: () => void;
    isExpanded?: boolean;
    onToggleExpand?: () => void;
    className?: string;
    indent?: boolean;
    hasActiveChild?: boolean;
    isCollapsed?: boolean;
}

export const NavItemButton = React.forwardRef<HTMLElement, NavItemButtonProps & Omit<React.HTMLAttributes<HTMLElement>, "onClick">>(function NavItemButton(
    { item, isActive, onClick, isExpanded, onToggleExpand, className, indent, hasActiveChild, isCollapsed = false, ...rest },
    ref
) {
    const isHighlighted = isActive || hasActiveChild;
    const classes = cn(navButtonStyles({ indent: !!indent, active: !!indent && isActive }), className);
    const handleClick = item.hasSubmenu ? onToggleExpand : onClick;

    const children = (
        <>
            {indent ? (
                <>
                    {item.icon && <item.icon className={cn("mr-1.5 h-4 w-4 shrink-0", isActive ? "text-(--brand-wMain)" : "text-(--secondary-text-wMain)")} />}
                    <span className={navTextStyles({ active: isActive })}>{item.label}</span>
                </>
            ) : (
                <>
                    <div className={iconWrapperStyles({ active: isHighlighted, withMargin: true })}>{item.icon && <item.icon className={iconStyles({ active: isHighlighted })} />}</div>
                    <span className={cn(navTextStyles({ active: isActive }), "overflow-hidden whitespace-nowrap transition-[opacity,max-width] duration-200", isCollapsed ? "max-w-0 opacity-0" : "max-w-[180px] opacity-100")}>{item.label}</span>
                </>
            )}
            {!isCollapsed && item.hasSubmenu && <span className="ml-auto text-(--darkSurface-text)">{isExpanded ? <ChevronUp className="size-6" /> : <ChevronDown className="size-6" />}</span>}
            {!isCollapsed && item.badge && <span className={isActive ? "text-(--darkSurface-text)" : "text-(--darkSurface-textMuted)"}>{item.badge}</span>}
        </>
    );

    if (item.route && !item.hasSubmenu) {
        return (
            <Link ref={ref as React.Ref<HTMLAnchorElement>} to={item.route} {...rest} onClick={handleClick} className={classes}>
                {children}
            </Link>
        );
    }

    return (
        <button ref={ref as React.Ref<HTMLButtonElement>} type="button" {...rest} onClick={handleClick} className={classes}>
            {children}
        </button>
    );
});
