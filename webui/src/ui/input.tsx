// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ComponentProps } from "react";

import { cn } from "@/utils";

function Input({ className, type, ...props }: ComponentProps<"input">) {
    return (
        <input
            type={type}
            data-slot="input"
            className={cn(
                "flex h-9 w-full min-w-0",

                "rounded-xs border bg-transparent",

                "px-3 py-1",

                "text-base md:text-sm",

                "shadow-xs transition-[color,box-shadow] outline-none",

                "placeholder:text-(--secondary-wMain)",

                "focus-visible:border-(--brand-wMain)",

                "disabled:cursor-not-allowed disabled:opacity-50",

                "read-only:cursor-default read-only:opacity-80",

                "aria-invalid:border-(--error-w100)",

                className
            )}
            {...props}
        />
    );
}

export { Input };
