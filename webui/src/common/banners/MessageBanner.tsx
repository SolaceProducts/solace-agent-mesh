// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { AlertCircle, AlertTriangle, CircleCheckBig, Info, X } from "lucide-react";

import { Button } from "@/ui/button";

import { cn } from "@/utils";

import { messageColourVariants } from "./messageColourVariants";

const messageBannerVariants = cva("flex items-center gap-3 text-sm font-medium transition-all border-l-4 border-solid ", {
    variants: { variant: messageColourVariants },
    defaultVariants: {
        variant: "error",
    },
});

const iconMap = {
    error: AlertCircle,
    warning: AlertTriangle,
    info: Info,
    success: CircleCheckBig,
};

const iconColorMap: Record<string, string> = {
    warning: "text-(--warning-w100)",
};

const textColorOverrides: Record<string, string> = {
    warning: "text-(--primary-text-wMain)",
};

export interface MessageBannerBaseProps extends React.HTMLAttributes<HTMLDivElement>, VariantProps<typeof messageBannerVariants> {
    message: string | React.ReactNode;
    icon?: React.ReactNode;
    dismissible?: boolean;
    onDismiss?: () => void;
}

export type MessageBannerProps = MessageBannerBaseProps;

function MessageBanner({ className, variant = "error", message, icon, dismissible = false, onDismiss, ...props }: MessageBannerProps) {
    const IconComponent = iconMap[variant || "error"];

    return (
        <div data-testid="messageBanner" className={cn(messageBannerVariants({ variant, className }), "items-start", textColorOverrides[variant || "error"])} role="alert" aria-live="polite" {...props}>
            <div className="flex flex-1 items-start gap-3 px-3 py-2">
                {icon || <IconComponent className={cn("size-5 shrink-0", iconColorMap[variant || "error"])} />}
                {typeof message === "string" ? <span>{message}</span> : message}
            </div>

            <div className="ml-auto flex items-center gap-1">
                {dismissible && onDismiss && (
                    <Button type="button" variant="ghost" size="icon" onClick={onDismiss} aria-label="Dismiss">
                        <X className="size-6" />
                    </Button>
                )}
            </div>
        </div>
    );
}

export { MessageBanner };
