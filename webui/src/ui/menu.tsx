// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import * as React from "react";

import { cn } from "@/utils";

interface MenuAction {
    id: string;
    label: string;
    icon?: React.ReactNode;
    iconPosition?: "left" | "right";
    onClick: () => void;
    divider?: boolean;
    disabled?: boolean;
}

interface MenuProps {
    actions: MenuAction[];
    className?: string;
}

const Menu = React.forwardRef<HTMLDivElement, MenuProps>(({ actions, className, ...props }, ref) => {
    const handleKeyDown = (event: React.KeyboardEvent, action: MenuAction) => {
        if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            if (!action.disabled) action.onClick();
        }
    };

    const handleItemKeyDown = (event: React.KeyboardEvent, index: number) => {
        const items = event.currentTarget.parentElement?.querySelectorAll('[role="menuitem"]');
        if (!items) return;

        switch (event.key) {
            case "ArrowDown": {
                event.preventDefault();
                const nextIndex = (index + 1) % items.length;
                (items[nextIndex] as HTMLElement)?.focus();
                break;
            }
            case "ArrowUp": {
                event.preventDefault();
                const prevIndex = (index - 1 + items.length) % items.length;
                (items[prevIndex] as HTMLElement)?.focus();
                break;
            }
            case "Home":
                event.preventDefault();
                (items[0] as HTMLElement)?.focus();
                break;
            case "End":
                event.preventDefault();
                (items[items.length - 1] as HTMLElement)?.focus();
                break;
            case "Escape":
                event.preventDefault();
                (event.currentTarget as HTMLElement).blur();
                break;
        }
    };

    return (
        <div ref={ref} role="menu" className={cn("min-w-[8rem] overflow-hidden", className)} {...props}>
            {actions.map((action, index) => (
                <React.Fragment key={action.id}>
                    {action.divider && index > 0 && <div className="my-1 h-px bg-(--secondary-w40)" />}
                    <div
                        role="menuitem"
                        tabIndex={0}
                        data-disabled={action.disabled || undefined}
                        aria-disabled={action.disabled || undefined}
                        className={cn(
                            "relative my-1.5 flex cursor-pointer items-center gap-2 px-3 py-1.5 text-sm transition-colors select-none",
                            "data-[disabled]:pointer-events-none data-[disabled]:opacity-50",
                            "hover:bg-(--primary-w10) hover:text-(--primary-text-w60)"
                        )}
                        onClick={action.disabled ? undefined : action.onClick}
                        onKeyDown={e => {
                            handleKeyDown(e, action);
                            handleItemKeyDown(e, index);
                        }}
                    >
                        {action.icon && action.iconPosition !== "right" && <span className="flex h-4 w-4 items-center justify-center">{action.icon}</span>}
                        <span className="flex-1">{action.label}</span>
                        {action.icon && action.iconPosition === "right" && <span className="flex h-4 w-4 items-center justify-center">{action.icon}</span>}
                    </div>
                </React.Fragment>
            ))}
        </div>
    );
});

Menu.displayName = "Menu";

export { Menu, type MenuAction, type MenuProps };
