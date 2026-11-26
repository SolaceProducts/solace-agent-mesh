// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import * as React from "react";
import * as TabsPrimitive from "@radix-ui/react-tabs";

import { cn } from "@/utils";

type TabsVariant = "pill" | "underline";

const TabsVariantContext = React.createContext<TabsVariant>("pill");

const tabsListVariants: Record<TabsVariant, string> = {
    pill: "inline-flex w-fit items-center overflow-hidden rounded-lg border",
    underline: "flex h-auto w-full items-center justify-start gap-6 rounded-none border-b border-(--secondary-w20) bg-transparent p-0",
};

const tabsTriggerVariants: Record<TabsVariant, string> = {
    pill: "rounded-none border-r px-4 py-2 last:border-r-0 hover:bg-(--secondary-w20) disabled:text-(--secondary-text-w50) data-[state=active]:bg-(--primary-w20) data-[state=active]:font-semibold",
    underline:
        "-mb-px rounded-none border-0 border-b-2 border-transparent px-1 pb-2 text-(--secondary-text-wMain) hover:text-(--primary-text-wMain) data-[state=active]:border-(--brand-wMain) data-[state=active]:font-semibold data-[state=active]:text-(--primary-text-wMain)",
};

function Tabs({ className, variant = "pill", ...props }: React.ComponentProps<typeof TabsPrimitive.Root> & { variant?: TabsVariant }) {
    return (
        <TabsVariantContext.Provider value={variant}>
            <TabsPrimitive.Root data-slot="tabs" className={cn("flex flex-col gap-2", className)} {...props} />
        </TabsVariantContext.Provider>
    );
}

function TabsList({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.List>) {
    const variant = React.useContext(TabsVariantContext);
    return <TabsPrimitive.List data-slot="tabs-list" className={cn(tabsListVariants[variant], className)} {...props} />;
}

function TabsTrigger({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.Trigger>) {
    const variant = React.useContext(TabsVariantContext);
    return (
        <TabsPrimitive.Trigger
            data-slot="tabs-trigger"
            data-testid={props.value}
            title={props.title}
            className={cn(
                "inline-flex cursor-pointer items-center justify-center gap-1.5 text-sm font-medium whitespace-nowrap transition-[color,box-shadow,background-color] focus-visible:-outline-offset-1 disabled:pointer-events-none [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
                tabsTriggerVariants[variant],
                className
            )}
            {...props}
            aria-label={props["aria-label"] || props.title || props.value}
        />
    );
}

function TabsContent({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.Content>) {
    return <TabsPrimitive.Content data-slot="tabs-content" className={cn("flex-1 outline-none", className)} {...props} />;
}

export { Tabs, TabsContent, TabsList, TabsTrigger };
