// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { TriangleAlert } from "lucide-react";

import type { DroppedResource, RemovalResult } from "@/api/builder/applyTypes";

import { Button } from "@/ui/button";

import { deploymentPhrase, type DroppedView, kindLabel, pageLabel, removableResources } from "./droppedResources";
import { RemoveDroppedDialog } from "./RemoveDroppedDialog";

interface DroppedResourcesNoticeProps {
    view: DroppedView;
    onRemove: (resources: readonly DroppedResource[]) => Promise<RemovalResult[]>;
}

export function DroppedResourcesNotice({ view, onRemove }: Readonly<DroppedResourcesNoticeProps>) {
    const [confirming, setConfirming] = useState<DroppedResource[] | null>(null);

    return (
        <>
            <Listing view={view} onAskToRemove={setConfirming} />
            <RemoveDroppedDialog snapshot={confirming} onRemove={onRemove} onClose={() => setConfirming(null)} />
        </>
    );
}

function Listing({ view, onAskToRemove }: Readonly<{ view: DroppedView; onAskToRemove: (snapshot: DroppedResource[]) => void }>) {
    const incomplete = view.checkFailed ? (
        <p role="status" className="mt-3 flex items-start gap-2 rounded-md border bg-(--background-w10) p-3 text-sm">
            <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0 text-(--warning-wMain)" />
            The platform could not finish checking which components this plan dropped, so a component that is still deployed may not be listed. Check the Agents and Workflows pages.
        </p>
    ) : null;
    if (view.resources.length === 0) return incomplete;

    const removable = removableResources(view.resources);
    const count = view.resources.length;

    return (
        <>
            <ListedResources view={view} removable={removable} count={count} onAskToRemove={onAskToRemove} />
            {incomplete}
        </>
    );
}

function ListedResources({ view, removable, count, onAskToRemove }: Readonly<{ view: DroppedView; removable: readonly DroppedResource[]; count: number; onAskToRemove: (snapshot: DroppedResource[]) => void }>) {
    return (
        <section aria-labelledby="dropped-resources-heading" className="mt-3 flex flex-col gap-3 rounded-md border bg-(--background-w10) p-4">
            <div className="flex items-start gap-3">
                <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0 text-(--warning-wMain)" />
                <div className="flex min-w-0 flex-col gap-2 text-sm leading-relaxed">
                    <h4 id="dropped-resources-heading" className="font-medium">
                        {count === 1 ? "A component this build left in place" : `${count} components this build left in place`}
                    </h4>
                    <p>
                        The plan no longer includes {count === 1 ? "it" : "them"}, but Build &amp; Activate does not remove anything, so {count === 1 ? "it is" : "they are"} as {count === 1 ? "it was" : "they were"}:
                    </p>
                    <ul className="flex flex-col gap-1.5">
                        {view.resources.map(resource => (
                            <li key={resource.id}>
                                <span className="font-medium">{resource.name}</span> <span className="text-xs text-(--secondary-text-wMain)">({kindLabel(resource.kind)})</span>: {deploymentPhrase(resource.deployment)}.
                                {!resource.createdBySession && <> It existed before this session changed it, so remove it from the {pageLabel(resource.kind)} page.</>}
                                {view.failures.has(resource.id) && (
                                    <span role="alert" className="mt-0.5 block text-xs text-(--error-wMain)">
                                        {view.failures.get(resource.id)}
                                    </span>
                                )}
                            </li>
                        ))}
                    </ul>
                </div>
            </div>
            {removable.length > 0 && (
                <div className="flex justify-end">
                    <Button size="sm" variant="outline" disabled={view.status !== "ready"} onClick={() => onAskToRemove([...removable])}>
                        {removeLabel(removable, view)}
                    </Button>
                </div>
            )}
        </section>
    );
}

function removeLabel(removable: readonly DroppedResource[], view: DroppedView): string {
    if (view.status !== "ready") return "Checking…";
    const retry = removable.some(r => view.failures.has(r.id));
    const what = removable.length === 1 ? removable[0].name : `${removable.length} components`;
    return retry ? `Retry removing ${what}…` : `Remove ${what}…`;
}
