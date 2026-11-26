// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cn } from "@/utils";

export const ActivityDot = ({ className }: { className?: string }) => <span className={cn("brand-breathe inline-block size-2.5 flex-shrink-0 rounded-full bg-(--success-wMain)", className)} aria-label="Working" />;
