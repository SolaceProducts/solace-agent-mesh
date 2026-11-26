// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cva } from "class-variance-authority";

export const sessionCardStyles = cva(["group", "relative", "rounded-lg", "border", "p-4", "transition-colors"], {
    variants: {
        active: {
            true: "bg-(--secondary-w10)",
            false: "hover:bg-(--primary-w10)",
        },
        size: {
            default: "h-[75px]",
        },
    },
    defaultVariants: { active: false, size: "default" },
});

export const sessionTitleStyles = cva(["truncate", "text-sm", "transition-opacity", "duration-300"], {
    variants: {
        active: {
            true: "font-bold text-(--primary-text-wMain)",
            false: "font-bold text-(--primary-text-wMain)",
        },
        animation: {
            pulseGenerate: "animate-pulse-slow",
            pulseWait: "animate-pulse opacity-50",
            none: "opacity-100",
        },
    },
    defaultVariants: { active: false, animation: "none" },
});
