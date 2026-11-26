// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import * as TabsPrimitive from "@radix-ui/react-tabs";
import type { ComponentProps } from "react";

import { cn } from "@/utils/cnTailwind";

export function EvalTabsTrigger({ className, ...props }: ComponentProps<typeof TabsPrimitive.Trigger>) {
    return (
        <TabsPrimitive.Trigger
            className={cn(
                "box-border inline-flex h-full min-h-[48px] cursor-pointer items-center justify-center border-b-2 border-b-transparent px-4 py-2.5 text-sm font-medium text-(--secondary-text-wMain) select-none hover:text-(--primary-text-wMain) data-[state=active]:border-b-(--brand-w100) data-[state=active]:text-(--primary-text-wMain)",
                className
            )}
            {...props}
        />
    );
}
