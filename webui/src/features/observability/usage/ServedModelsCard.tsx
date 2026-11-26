// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageServedModel } from "@/api/usage";

import { formatUsageMoney } from "@/utils";

import { formatCount } from "../feedbackFormat";

import { servedModelKey } from "./usageKinds";

const MODEL_NOT_RECORDED = "Model not recorded";

export function ServedModelsCard({ models }: Readonly<{ models: readonly UsageServedModel[] }>) {
    return (
        <section aria-label="Provider Models" className="flex flex-col gap-3 rounded-sm border p-4">
            <h3 className="text-sm font-semibold">Provider Models</h3>
            {models.length === 0 ? (
                <p className="text-sm text-(--secondary-text-wMain)">No usage in this period</p>
            ) : (
                <ol className="flex flex-col text-sm">
                    {models.map(model => (
                        <li key={servedModelKey(model)} className="flex items-center gap-2 border-b py-1.5 last:border-b-0">
                            <span className="flex min-w-0 flex-1 flex-col">
                                {model.name === null ? (
                                    <span className="text-(--secondary-text-wMain)">{MODEL_NOT_RECORDED}</span>
                                ) : (
                                    <>
                                        <span className="truncate font-medium" title={model.name}>
                                            {model.name}
                                        </span>
                                        {model.provider && <span className="text-xs text-(--secondary-text-wMain)">{model.provider}</span>}
                                    </>
                                )}
                            </span>
                            <span className="shrink-0 text-xs text-(--secondary-text-wMain) tabular-nums">{formatCount(model.activities)} activities</span>
                            <span className="w-20 shrink-0 text-right font-medium tabular-nums">{formatUsageMoney(model.cost)}</span>
                        </li>
                    ))}
                </ol>
            )}
        </section>
    );
}
