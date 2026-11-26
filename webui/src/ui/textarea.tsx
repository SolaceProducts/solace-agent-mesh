// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { cn } from "@/utils";

const Textarea = React.forwardRef<HTMLTextAreaElement, React.TextareaHTMLAttributes<HTMLTextAreaElement>>(({ className, ...props }, ref) => {
    return (
        <textarea
            className={cn(
                "flex min-h-[80px] w-full min-w-0",

                "rounded-xs border bg-transparent",

                "px-3 py-2",

                "text-base md:text-sm",

                "shadow-xs transition-[color,box-shadow] outline-none",

                "placeholder:text-(--secondary-wMain)",

                "focus-visible:border-(--brand-wMain)",

                "disabled:cursor-not-allowed disabled:opacity-50",

                "read-only:cursor-default read-only:opacity-80",

                "aria-invalid:border-(--error-w100)",
                className
            )}
            ref={ref}
            {...props}
        />
    );
});
Textarea.displayName = "Textarea";

export { Textarea };
