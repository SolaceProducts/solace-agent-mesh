// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

"use client";

import * as React from "react";
import * as TooltipPrimitive from "@radix-ui/react-tooltip";

import { cn } from "@/utils";

const RichTooltipProviderSentinel = React.createContext(false);

function RichTooltipProvider({ delayDuration = 500, skipDelayDuration = 300, children, ...props }: Readonly<React.ComponentProps<typeof TooltipPrimitive.Provider>>) {
    return (
        <RichTooltipProviderSentinel.Provider value={true}>
            <TooltipPrimitive.Provider data-slot="rich-tooltip-provider" delayDuration={delayDuration} skipDelayDuration={skipDelayDuration} {...props}>
                {children}
            </TooltipPrimitive.Provider>
        </RichTooltipProviderSentinel.Provider>
    );
}

const SELF_CLOSE_DEBOUNCE_MS = 50;

function RichTooltip({ open: openProp, defaultOpen, onOpenChange, ...props }: Readonly<React.ComponentProps<typeof TooltipPrimitive.Root>>) {
    const [internalOpen, setInternalOpen] = React.useState(defaultOpen ?? false);
    const isControlled = openProp !== undefined;
    const open = isControlled ? openProp : internalOpen;
    const lastOpenAtRef = React.useRef(0);

    const handleOpenChange = React.useCallback(
        (next: boolean) => {
            if (next) {
                lastOpenAtRef.current = performance.now();
            } else if (performance.now() - lastOpenAtRef.current < SELF_CLOSE_DEBOUNCE_MS) {
                return;
            }
            if (!isControlled) setInternalOpen(next);
            onOpenChange?.(next);
        },
        [isControlled, onOpenChange]
    );

    const hasAncestorProvider = React.useContext(RichTooltipProviderSentinel);
    const root = <TooltipPrimitive.Root data-slot="rich-tooltip" open={open} onOpenChange={handleOpenChange} {...props} />;
    return hasAncestorProvider ? root : <RichTooltipProvider>{root}</RichTooltipProvider>;
}

function RichTooltipTrigger({ onPointerDown, onPointerEnter, onClick, ...props }: Readonly<React.ComponentProps<typeof TooltipPrimitive.Trigger>>) {
    return (
        <TooltipPrimitive.Trigger
            data-slot="rich-tooltip-trigger"
            onPointerEnter={event => {
                onPointerEnter?.(event);
                if (event.pointerType === "touch") return;
                const target = event.currentTarget;
                const { clientX, clientY, pointerId, pointerType } = event;
                requestAnimationFrame(() => {
                    if (!target.isConnected) return;
                    target.dispatchEvent(
                        new PointerEvent("pointermove", {
                            bubbles: true,
                            cancelable: true,
                            clientX,
                            clientY,
                            pointerId,
                            pointerType,
                        })
                    );
                });
            }}
            onPointerDown={event => {
                onPointerDown?.(event);
                if (!event.defaultPrevented) event.preventDefault();
            }}
            onClick={event => {
                onClick?.(event);
                if (!event.defaultPrevented) event.preventDefault();
            }}
            {...props}
        />
    );
}

function RichTooltipContent({ className, sideOffset = 14, side = "top", align = "center", onPointerDownOutside, children, ...props }: Readonly<React.ComponentProps<typeof TooltipPrimitive.Content>>) {
    return (
        <TooltipPrimitive.Portal>
            <TooltipPrimitive.Content
                data-slot="rich-tooltip-content"
                side={side}
                align={align}
                sideOffset={sideOffset}
                onPointerDownOutside={event => {
                    onPointerDownOutside?.(event);
                    if (!event.defaultPrevented) event.preventDefault();
                }}
                className={cn(
                    "animate-in fade-in-0 zoom-in-95",
                    "data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95",
                    "data-[state=closed]:duration-1000",
                    "data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2",
                    "data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2",
                    "data-[state=closed]:pointer-events-none",
                    "z-[55] origin-(--radix-tooltip-content-transform-origin)",
                    "rounded-sm border border-(--secondary-w20) p-4 text-sm",
                    "shadow-[0_2px_5px_0_rgba(0,0,0,0.15)]",
                    "bg-(--background-wMain)",
                    "text-(--primary-text-w10)",
                    className
                )}
                {...props}
            >
                {children}
            </TooltipPrimitive.Content>
        </TooltipPrimitive.Portal>
    );
}

export { RichTooltip, RichTooltipContent, RichTooltipProvider, RichTooltipTrigger };
