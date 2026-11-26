// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { EvalRunStatus } from "@/types/eval";

import { EvalStatusBadge } from "./EvalStatusBadge";

export function ExperimentLastRun({ status }: { readonly status?: EvalRunStatus }) {
    if (!status) return <span className="text-xs text-(--secondary-text-wMain)">Never</span>;
    return <EvalStatusBadge status={status} />;
}
