// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cn } from "@/utils";

const RING_SIZE = 20;
const RING_STROKE = 2.5;
const RING_RADIUS = (RING_SIZE - RING_STROKE) / 2;
const RING_CIRCUMFERENCE = 2 * Math.PI * RING_RADIUS;

interface UsageRingProps {
    value: number;
    className?: string;
    trackClassName?: string;
}

export function UsageRing({ value, className, trackClassName = "stroke-(--secondary-w20)" }: Readonly<UsageRingProps>) {
    const filledFraction = Math.min(Math.max(value, 0), 100) / 100;
    const center = RING_SIZE / 2;
    return (
        <svg viewBox={`0 0 ${RING_SIZE} ${RING_SIZE}`} className={cn("size-5 shrink-0 -rotate-90", className)} aria-hidden="true">
            <circle cx={center} cy={center} r={RING_RADIUS} fill="none" strokeWidth={RING_STROKE} className={trackClassName} />
            <circle cx={center} cy={center} r={RING_RADIUS} fill="none" stroke="currentColor" strokeWidth={RING_STROKE} strokeLinecap="round" strokeDasharray={RING_CIRCUMFERENCE} strokeDashoffset={RING_CIRCUMFERENCE * (1 - filledFraction)} />
        </svg>
    );
}
