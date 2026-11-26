// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import * as React from "react";
import { EllipsisVerticalIcon } from "lucide-react";
import * as ResizablePrimitive from "react-resizable-panels";

import { cn } from "@/utils";

function ResizablePanelGroup({ className, ...props }: React.ComponentProps<typeof ResizablePrimitive.Group>) {
    return <ResizablePrimitive.Group data-slot="resizable-panel-group" className={cn("flex h-full w-full", className)} {...props} />;
}

function ResizablePanel({ className, style, ...props }: React.ComponentProps<typeof ResizablePrimitive.Panel>) {
    return <ResizablePrimitive.Panel data-slot="resizable-panel" className={cn("bg-transparent", className)} style={{ overflow: "hidden", ...style }} {...props} />;
}

function ResizableHandle({
    withHandle,
    className,
    ...props
}: React.ComponentProps<typeof ResizablePrimitive.Separator> & {
    withHandle?: boolean;
}) {
    return (
        <ResizablePrimitive.Separator
            data-slot="resizable-handle"
            className={cn(
                "relative flex w-px items-center justify-center bg-(--secondary-w40) after:absolute after:inset-y-0 after:left-1/2 after:w-1 after:-translate-x-1/2 focus-visible:ring-1 focus-visible:ring-(--brand-wMain) focus-visible:ring-offset-1 focus-visible:outline-hidden aria-[orientation=horizontal]:h-px aria-[orientation=horizontal]:w-full aria-[orientation=horizontal]:after:left-0 aria-[orientation=horizontal]:after:h-1 aria-[orientation=horizontal]:after:w-full aria-[orientation=horizontal]:after:translate-x-0 aria-[orientation=horizontal]:after:-translate-y-1/2 [&[aria-orientation=horizontal]>div]:rotate-90",
                className
            )}
            {...props}
        >
            {withHandle && (
                <div className="z-10 flex h-4 w-3 items-center justify-center rounded-xs border bg-(--secondary-w40)">
                    <EllipsisVerticalIcon className="size-2.5" />
                </div>
            )}
        </ResizablePrimitive.Separator>
    );
}

export { ResizableHandle, ResizablePanel, ResizablePanelGroup };
