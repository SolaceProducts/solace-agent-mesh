// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { useIsMobile } from "@/hooks";
import { Sheet, SheetContent } from "@/ui";
import { cn } from "@/utils";

interface PageSidePanelWrapperProps {
    sidepanel: React.ReactNode;
    isSidePanelOpen: boolean;
    onClose?: () => void;
    onCloseComplete?: () => void;
    className?: string;
    variant?: "rail" | "overlay";
}

const CLOSE_FALLBACK_MS = 1_000;

function useDeferredOpen(isOpen: boolean, hasContent: boolean, properties: readonly string[], onCloseComplete?: () => void) {
    const [deferredOpen, setDeferredOpen] = React.useState(false);
    const [mounted, setMounted] = React.useState(false);
    const closing = React.useRef(false);
    const onClosed = React.useRef(onCloseComplete);
    React.useEffect(() => {
        onClosed.current = onCloseComplete;
    }, [onCloseComplete]);

    const finishClose = React.useCallback(() => {
        if (!closing.current) return;
        closing.current = false;
        setMounted(false);
        onClosed.current?.();
    }, []);

    React.useEffect(() => {
        if (isOpen && hasContent) {
            closing.current = false;
            setMounted(true);
            const frame = requestAnimationFrame(() => {
                setDeferredOpen(true);
            });
            return () => cancelAnimationFrame(frame);
        }
        if (!mounted) return undefined;
        closing.current = true;
        if (!deferredOpen) {
            finishClose();
            return undefined;
        }
        setDeferredOpen(false);
        const fallback = setTimeout(finishClose, CLOSE_FALLBACK_MS);
        return () => clearTimeout(fallback);
    }, [isOpen, hasContent, finishClose]);

    const handleTransitionEnd = React.useCallback(
        (e: React.TransitionEvent) => {
            if (e.target === e.currentTarget && properties.includes(e.propertyName) && !deferredOpen) finishClose();
        },
        [deferredOpen, finishClose, properties]
    );

    return { mounted, deferredOpen, handleTransitionEnd };
}

const RAIL_PROPERTIES = ["width"] as const;
const SLIDE_PROPERTIES = ["translate", "transform"] as const;

const OverlayPanel = ({ sidepanel, isSidePanelOpen, onClose, onCloseComplete, className }: Omit<PageSidePanelWrapperProps, "variant">) => {
    const { mounted, deferredOpen, handleTransitionEnd } = useDeferredOpen(isSidePanelOpen, !!sidepanel, SLIDE_PROPERTIES, onCloseComplete);
    if (!mounted) return null;
    return (
        <>
            <div aria-hidden data-testid="sidePanelBackdrop" className={cn("overlay-backdrop absolute inset-0 z-20 transition-opacity duration-500", deferredOpen ? "opacity-100" : "pointer-events-none opacity-0")} onClick={onClose} />
            <div
                className={cn("absolute inset-y-0 right-0 z-20 w-[min(64rem,calc(100%-4rem))] shadow-lg transition-transform duration-500 ease-in-out", deferredOpen ? "translate-x-0" : "translate-x-full", className)}
                onTransitionEnd={handleTransitionEnd}
            >
                {sidepanel}
            </div>
        </>
    );
};

const DesktopPanel = ({ sidepanel, isSidePanelOpen, onCloseComplete, className }: Omit<PageSidePanelWrapperProps, "onClose" | "variant">) => {
    const { mounted, deferredOpen, handleTransitionEnd } = useDeferredOpen(isSidePanelOpen, !!sidepanel, RAIL_PROPERTIES, onCloseComplete);

    if (!mounted) return null;

    return (
        <div className={`h-full overflow-hidden transition-all duration-500 ease-in-out ${deferredOpen ? "w-full sm:w-1/4 sm:min-w-[400px]" : "w-0"} ${className ?? ""}`} onTransitionEnd={handleTransitionEnd}>
            <div className={`h-full transition-opacity duration-500 ${deferredOpen ? "opacity-100" : "pointer-events-none opacity-0"}`}>{sidepanel}</div>
        </div>
    );
};

export const PageSidePanelWrapper = ({ sidepanel, isSidePanelOpen, onClose, onCloseComplete, className = "", variant = "rail" }: PageSidePanelWrapperProps) => {
    const isMobile = useIsMobile();

    if (isMobile) {
        return (
            <Sheet
                open={isSidePanelOpen && !!sidepanel}
                onOpenChange={open => {
                    if (!open) {
                        onClose?.();
                        setTimeout(() => onCloseComplete?.(), 300);
                    }
                }}
            >
                <SheetContent side="right" className="w-full p-0" showClose={false}>
                    <div className="h-full overflow-y-auto">{sidepanel}</div>
                </SheetContent>
            </Sheet>
        );
    }

    if (variant === "overlay") return <OverlayPanel sidepanel={sidepanel} isSidePanelOpen={isSidePanelOpen} onClose={onClose} onCloseComplete={onCloseComplete} className={className} />;
    return <DesktopPanel sidepanel={sidepanel} isSidePanelOpen={isSidePanelOpen} onCloseComplete={onCloseComplete} className={className} />;
};
