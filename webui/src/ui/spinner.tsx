// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import type { VariantProps } from "class-variance-authority";
import { cva } from "class-variance-authority";
import { Loader2 } from "lucide-react";

import { cn } from "@/utils";

const spinnerVariants = cva("flex-col items-center justify-center", {
    variants: {
        show: {
            true: "flex",
            false: "hidden",
        },
    },
    defaultVariants: {
        show: true,
    },
});

const loaderVariants = cva("animate-spin", {
    variants: {
        size: {
            small: "size-4",
            medium: "size-8",
            large: "size-12",
        },
        variant: {
            primary: "text-(--brand-wMain)",
            secondary: "text-(--primary-wMain)",
            muted: "text-(--secondary-text-wMain)",
            foreground: "text-(--primary-text-wMain)",
        },
    },
    defaultVariants: {
        size: "medium",
        variant: "primary",
    },
});

interface SpinnerContentProps extends VariantProps<typeof spinnerVariants>, VariantProps<typeof loaderVariants> {
    className?: string;
    children?: React.ReactNode;
}

export function Spinner({ size, show, children, className, variant }: SpinnerContentProps) {
    return (
        <span className={spinnerVariants({ show })}>
            <Loader2 className={cn(loaderVariants({ size, variant }), className)} />
            {children}
        </span>
    );
}
