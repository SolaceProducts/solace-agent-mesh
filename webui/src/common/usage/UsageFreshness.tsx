// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";

import { cn } from "@/utils";

import { formatFreshness } from "./usageFormat";

const TICK_MS = 30_000;

function useNow(): Date {
    const [now, setNow] = useState(() => new Date());
    useEffect(() => {
        const id = globalThis.setInterval(() => setNow(new Date()), TICK_MS);
        return () => globalThis.clearInterval(id);
    }, []);
    return now;
}

interface UsageFreshnessProps {
    dataAsOf: string | null;
    historyComplete: boolean;
    className?: string;
}

export function UsageFreshness({ dataAsOf, historyComplete, className }: Readonly<UsageFreshnessProps>) {
    const text = formatFreshness(dataAsOf, historyComplete, useNow());
    return (
        <p className={cn("text-right text-xs text-(--secondary-text-wMain)", className)} data-testid="usageFreshness">
            {text}
        </p>
    );
}
