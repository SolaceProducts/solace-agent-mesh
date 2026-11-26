// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { CircleCheckBig, Loader2, Trash2, TriangleAlert } from "lucide-react";

import type { DroppedResource, RemovalResult } from "@/api/builder/applyTypes";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";

import { deploymentPhrase, kindLabel, removalProblem } from "./droppedResources";

interface RemoveDroppedDialogProps {
    snapshot: readonly DroppedResource[] | null;
    onRemove: (resources: readonly DroppedResource[]) => Promise<RemovalResult[]>;
    onClose: () => void;
}

const isGone = (result: RemovalResult | undefined) => result?.status === "removed" || result?.status === "not_found";

export function RemoveDroppedDialog({ snapshot, onRemove, onClose }: Readonly<RemoveDroppedDialogProps>) {
    const items = snapshot ?? [];
    const [results, setResults] = useState<ReadonlyMap<string, RemovalResult>>(new Map());
    const [busy, setBusy] = useState(false);
    const [requestError, setRequestError] = useState<string | null>(null);

    const remaining = items.filter(item => !isGone(results.get(item.id)));
    const attempted = results.size > 0 || requestError !== null;
    const allGone = items.length > 0 && remaining.length === 0;

    const close = () => {
        if (busy) return;
        setResults(new Map());
        setRequestError(null);
        onClose();
    };

    const remove = async () => {
        setBusy(true);
        setRequestError(null);
        try {
            const outcome = await onRemove(remaining);
            setResults(prev => new Map([...prev, ...outcome.map(r => [r.id, r] as const)]));
        } catch (err) {
            setRequestError(err instanceof Error ? err.message : "the request failed");
        } finally {
            setBusy(false);
        }
    };

    return (
        <Dialog open={items.length > 0} onOpenChange={open => !open && close()}>
            <DialogContent className="bg-(--background-w10) p-6 sm:max-w-lg" onInteractOutside={e => e.preventDefault()}>
                <DialogHeader>
                    <DialogTitle className="flex items-center gap-2">
                        <Trash2 className="h-5 w-5 text-(--error-wMain)" />
                        Remove {items.length === 1 ? "this" : `these ${items.length}`} from the platform?
                    </DialogTitle>
                    <DialogDescription>
                        The build did not remove {items.length === 1 ? "it" : "them"}. Removing {items.length === 1 ? "it" : "them"} undeploys what is running and deletes {items.length === 1 ? "it" : "them"}, along with{" "}
                        {items.length === 1 ? "its" : "their"} deployment history. This cannot be undone.
                    </DialogDescription>
                </DialogHeader>
                <ul className="mt-2 max-h-60 space-y-2 overflow-y-auto text-sm" aria-live="polite">
                    {items.map(item => (
                        <ResourceRow key={item.id} item={item} result={results.get(item.id)} />
                    ))}
                </ul>
                {requestError && (
                    <p role="alert" className="mt-2 text-sm text-(--error-wMain)">
                        The request to remove {remaining.length === 1 ? "it" : "them"} failed: {requestError}
                    </p>
                )}
                <DialogFooter className="mt-4">
                    <Button size="sm" variant="ghost" onClick={close} disabled={busy}>
                        {allGone || attempted ? "Close" : "Cancel"}
                    </Button>
                    {!allGone && (
                        <Button size="sm" onClick={() => void remove()} disabled={busy}>
                            {busy && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
                            {busy ? "Removing…" : actionLabel(attempted, remaining.length)}
                        </Button>
                    )}
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

function actionLabel(attempted: boolean, count: number): string {
    if (attempted) return count === 1 ? "Retry removal" : `Retry removal of ${count}`;
    return count === 1 ? "Remove" : `Remove ${count}`;
}

function ResourceRow({ item, result }: Readonly<{ item: DroppedResource; result: RemovalResult | undefined }>) {
    return (
        <li className="rounded-md border border-(--secondary-w20) p-2">
            <div className="font-medium">
                {item.name} <span className="text-xs font-normal text-(--secondary-text-wMain)">({kindLabel(item.kind)})</span>
            </div>
            {result === undefined && <div className="text-xs text-(--secondary-text-wMain)">{capitalize(deploymentPhrase(item.deployment))}</div>}
            {isGone(result) && (
                <div className="flex items-center gap-1 text-xs text-(--success-wMain)">
                    <CircleCheckBig className="h-3.5 w-3.5" />
                    {result?.status === "removed" ? "Removed" : "Already gone"}
                </div>
            )}
            {result !== undefined && !isGone(result) && (
                <div role="alert" className="flex items-start gap-1 text-xs text-(--error-wMain)">
                    <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                    {removalProblem(result)}
                </div>
            )}
        </li>
    );
}

function capitalize(text: string): string {
    return text.charAt(0).toUpperCase() + text.slice(1);
}
