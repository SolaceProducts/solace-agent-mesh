// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type FocusEvent, type KeyboardEvent, useRef, useState } from "react";
import { HelpCircle } from "lucide-react";

import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";

const TABBABLE = ["a[href]", "button:not([disabled])", 'input:not([disabled]):not([type="hidden"])', "select:not([disabled])", "textarea:not([disabled])", "[tabindex]"].map(selector => `${selector}:not([tabindex="-1"])`).join(", ");

function canTakeFocus(element: HTMLElement): boolean {
    return !element.closest("[inert]") && (element.checkVisibility?.() ?? true);
}

function tabbablesIn(root: ParentNode | null): HTMLElement[] {
    return root ? [...root.querySelectorAll<HTMLElement>(TABBABLE)].filter(canTakeFocus) : [];
}

function nextTabbableAfter(trigger: HTMLElement, content: HTMLElement | null): HTMLElement | undefined {
    const order = tabbablesIn(trigger.ownerDocument).filter(element => !content?.contains(element));
    return order[order.indexOf(trigger) + 1];
}

export const HelpPopover = ({
    content,
    className,
    side = "top",
    align = "center",
    size = "20",
    label = "Help",
}: {
    content: React.ReactNode;
    className?: string;
    side?: "top" | "bottom" | "left" | "right";
    align?: "start" | "center" | "end";
    size?: string;
    label?: string;
}) => {
    const [open, setOpen] = useState(false);
    const triggerRef = useRef<HTMLButtonElement>(null);
    const contentRef = useRef<HTMLDivElement>(null);
    const reopenOnFocus = useRef(true);

    const isWithin = (target: EventTarget | null) => target instanceof Node && (triggerRef.current?.contains(target) || contentRef.current?.contains(target));
    const closeUnlessFocusStays = (event: FocusEvent) => {
        if (!isWithin(event.relatedTarget)) setOpen(false);
    };

    const onTriggerKeyDown = (event: KeyboardEvent) => {
        const first = tabbablesIn(contentRef.current)[0];
        if (event.key !== "Tab" || event.shiftKey || !open || !first) return;
        event.preventDefault();
        first.focus();
    };
    const onContentKeyDown = (event: KeyboardEvent) => {
        if (event.key !== "Tab" || !triggerRef.current) return;
        const tabbables = tabbablesIn(contentRef.current);
        if (event.shiftKey && document.activeElement === tabbables[0]) {
            event.preventDefault();
            triggerRef.current.focus();
        } else if (!event.shiftKey && document.activeElement === tabbables.at(-1)) {
            const next = nextTabbableAfter(triggerRef.current, contentRef.current);
            setOpen(false);
            if (!next) return;
            event.preventDefault();
            next.focus();
            if (document.activeElement !== next) focusTriggerWithoutReopening();
        }
    };
    const focusTriggerWithoutReopening = () => {
        reopenOnFocus.current = false;
        triggerRef.current?.focus();
        reopenOnFocus.current = true;
    };
    const onEscape = () => {
        if (contentRef.current?.contains(document.activeElement)) focusTriggerWithoutReopening();
    };

    return (
        <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger asChild onMouseEnter={() => setOpen(true)} onMouseLeave={() => setOpen(false)} onFocus={() => reopenOnFocus.current && setOpen(true)} onBlur={closeUnlessFocusStays} onKeyDown={onTriggerKeyDown}>
                <button ref={triggerRef} type="button" aria-label={label} className="inline-flex cursor-help items-center text-(--secondary-text-wMain)">
                    <HelpCircle size={size} />
                </button>
            </PopoverTrigger>
            <PopoverContent
                side={side}
                align={align}
                className={className}
                onMouseEnter={() => setOpen(true)}
                onMouseLeave={() => setOpen(false)}
                onBlur={closeUnlessFocusStays}
                onKeyDown={onContentKeyDown}
                onEscapeKeyDown={onEscape}
                onOpenAutoFocus={event => event.preventDefault()}
                onCloseAutoFocus={event => event.preventDefault()}
            >
                <div ref={contentRef} className="max-w-100 p-4 text-sm leading-5 text-(--primary-text-wMain)">
                    {content}
                </div>
            </PopoverContent>
        </Popover>
    );
};
