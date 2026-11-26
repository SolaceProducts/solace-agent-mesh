// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ReactNode } from "react";

import { cn } from "@/utils";

interface ActivationBackdropProps {
    children?: ReactNode;
    wide?: boolean;
}

export function ActivationBackdrop({ children, wide = false }: Readonly<ActivationBackdropProps>) {
    return (
        <div className="relative flex h-screen w-screen items-center justify-center overflow-hidden p-4">
            <div aria-hidden="true" className="absolute inset-0 bg-(image:--activation-backdrop) bg-size-[100%_100%] bg-no-repeat" />
            <div aria-hidden="true" className="overlay-backdrop absolute inset-0" />
            <div className={cn("relative max-h-full w-full overflow-y-auto", wide ? "max-w-2xl" : "max-w-lg")}>{children}</div>
        </div>
    );
}
