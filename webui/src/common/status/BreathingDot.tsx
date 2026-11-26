// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cn } from "@/utils";

export const BreathingDot = ({ className }: { className?: string }) => <span className={cn("brand-breathe inline-block size-7 rounded-full bg-(--brand-wMain)", className)} aria-label="Waiting for response" />;
