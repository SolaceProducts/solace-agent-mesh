// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cn } from "@/utils";

function Skeleton({ className, ...props }: React.ComponentProps<"div">) {
    return <div data-slot="skeleton" className={cn("animate-pulse rounded-md bg-(--secondary-w40)", className)} {...props} />;
}

export { Skeleton };
