// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";
import { useFormContext } from "react-hook-form";

import { MessageBanner } from "@/common/banners";
import { EmptySection } from "@/common/layout";

import { ConfirmationDialog } from "@/common";
import type { ConnectorSchema } from "@/types";

import { EventMeshInfoPanel } from "./components/EventMeshInfoPanel";
import { EventMeshToolForm } from "./components/EventMeshToolForm";
import { EventMeshToolRail } from "./components/EventMeshToolRail";
import { eventMeshDuplicateToolNameUids, type EventMeshOperation, eventMeshOperationIssues, newEventMeshOperation, newUid } from "./eventMeshUtils";

interface EventMeshConnectorWorkspaceProps {
    schemaInfo: ConnectorSchema;
    validationError?: string | null;
    onClearValidationError?: () => void;
    showValidation?: boolean;
}

export function EventMeshConnectorWorkspace({ schemaInfo, validationError, onClearValidationError, showValidation }: Readonly<EventMeshConnectorWorkspaceProps>) {
    const { watch, setValue } = useFormContext();
    const [selectedIndex, setSelectedIndex] = useState(0);
    const [deleteTarget, setDeleteTarget] = useState<{ index: number } | null>(null);
    const [search, setSearch] = useState("");
    const [railCollapsed, setRailCollapsed] = useState(false);

    const watchedOperations = watch("values.operations") as EventMeshOperation[] | undefined;
    const operations = useMemo(() => watchedOperations ?? [], [watchedOperations]);

    const activeIndex = operations.length === 0 ? -1 : Math.min(selectedIndex, operations.length - 1);
    const activeOperation = activeIndex >= 0 ? operations[activeIndex] : undefined;

    const duplicateToolNameUids = useMemo(() => eventMeshDuplicateToolNameUids(operations), [operations]);
    const invalidUids = useMemo(() => {
        if (!showValidation) return new Set<string>();
        const uids = new Set(operations.filter(op => eventMeshOperationIssues(op).length > 0).flatMap(op => (op._uid ? [op._uid] : [])));
        duplicateToolNameUids.forEach(uid => uids.add(uid));
        return uids;
    }, [operations, duplicateToolNameUids, showValidation]);

    const updateOperations = (next: EventMeshOperation[]) => {
        setValue("values.operations", next, { shouldDirty: true, shouldValidate: true });
        onClearValidationError?.();
    };

    const updateOperationAt = (index: number, patch: Partial<EventMeshOperation>) => {
        updateOperations(operations.map((operation, i) => (i === index ? { ...operation, ...patch } : operation)));
    };

    const addOperation = () => {
        const next = [...operations, newEventMeshOperation(operations.length)];
        updateOperations(next);
        setSelectedIndex(next.length - 1);
    };

    const duplicateOperation = (index: number) => {
        const operation = operations[index];
        if (!operation) return;
        const duplicate: EventMeshOperation = {
            ...operation,
            _uid: newUid(),
            tool_name: `${operation.tool_name}_copy`.slice(0, 64),
            input_schema: structuredClone(operation.input_schema),
        };
        const next = [...operations.slice(0, index + 1), duplicate, ...operations.slice(index + 1)];
        updateOperations(next);
        setSelectedIndex(index + 1);
    };

    const moveOperation = (index: number, direction: -1 | 1) => {
        const target = index + direction;
        if (target < 0 || target >= operations.length) return;
        const next = [...operations];
        [next[index], next[target]] = [next[target], next[index]];
        updateOperations(next);
        setSelectedIndex(target);
    };

    const removeTarget = () => {
        if (!deleteTarget) return;
        const next = operations.filter((_, index) => index !== deleteTarget.index);
        updateOperations(next);
        setSelectedIndex(Math.max(0, Math.min(deleteTarget.index, next.length - 1)));
        setDeleteTarget(null);
    };

    return (
        <div className="-mx-8 -mt-6 -mb-8">
            {(validationError || schemaInfo.warning) && (
                <div className="space-y-2 px-8 pb-4">
                    {validationError && <MessageBanner variant="error" message={validationError} />}
                    {schemaInfo.warning && <MessageBanner variant="info" message={schemaInfo.warning} />}
                </div>
            )}
            {operations.length === 0 ? (
                <div className="px-8 pb-8">
                    <EmptySection message="No tools yet." action="Create New Tool" onClick={addOperation} />
                </div>
            ) : (
                <div className="flex h-[calc(100vh-230px)] min-h-0 items-stretch overflow-hidden">
                    <EventMeshToolRail
                        operations={operations}
                        activeIndex={activeIndex}
                        search={search}
                        onSearchChange={setSearch}
                        collapsed={railCollapsed}
                        onCollapsedChange={setRailCollapsed}
                        onSelect={setSelectedIndex}
                        onAdd={addOperation}
                        onDuplicate={duplicateOperation}
                        onMove={moveOperation}
                        onRequestDelete={index => setDeleteTarget({ index })}
                        invalidUids={invalidUids}
                    />
                    <div className="flex min-h-0 flex-1 overflow-hidden">
                        <main className="min-w-[560px] flex-1 overflow-y-auto p-5 pl-8 xl:p-6 xl:pl-8">
                            {activeOperation && (
                                <EventMeshToolForm
                                    key={activeOperation._uid ?? activeIndex}
                                    operation={activeOperation}
                                    onChange={patch => updateOperationAt(activeIndex, patch)}
                                    hasDuplicateName={!!activeOperation._uid && duplicateToolNameUids.has(activeOperation._uid)}
                                    showValidation={showValidation}
                                />
                            )}
                        </main>
                        <EventMeshInfoPanel />
                    </div>
                </div>
            )}

            <ConfirmationDialog
                open={deleteTarget !== null}
                onOpenChange={open => !open && setDeleteTarget(null)}
                title="Remove tool?"
                description="This tool will be removed when you save the connector."
                actionLabels={{ confirm: "Remove" }}
                onConfirm={removeTarget}
            />
        </div>
    );
}
