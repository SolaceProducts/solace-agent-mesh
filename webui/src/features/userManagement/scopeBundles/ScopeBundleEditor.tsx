// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { Plus, X } from "lucide-react";

import { ConfirmationDialog } from "@/common/dialogs/ConfirmationDialog";

import { Button } from "@/ui/button";
import { ComboBox, type ComboBoxItem } from "@/ui/combobox";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/ui/tabs";

import { useIsFeedbackManagementEnabled } from "@/hooks";

import {
    addAllCard,
    addCard,
    addComponent,
    ALL_ID,
    type CardGrant,
    cardGrants,
    cardIds,
    componentIds,
    type ComponentKind,
    type ComponentOption,
    grantAllComponents,
    hasAllCard,
    hasAllComponents,
    removeCard,
    removeComponent,
    type ScopeBundleEditorProps,
    setCardGrant,
} from "./bundles";
import { GrantCard } from "./GrantCard";

function RemovableRow({ label, description, onRemove, disabled }: Readonly<{ label: string; description?: string; onRemove: () => void; disabled?: boolean }>) {
    return (
        <li className="flex items-center justify-between gap-2 rounded-xs border px-3 py-2">
            <span className="flex min-w-0 flex-1 flex-col gap-1">
                <span className="truncate" title={label}>
                    {label}
                </span>
                {description && (
                    <span className="truncate text-xs text-(--secondary-text-wMain)" title={description}>
                        {description}
                    </span>
                )}
            </span>
            {!disabled && (
                <Button variant="ghost" size="icon" tooltip="Remove" onClick={onRemove}>
                    <X />
                </Button>
            )}
        </li>
    );
}

function ComponentSection({
    label,
    description,
    addLabel,
    allLabel,
    emptyText,
    options,
    selectedIds,
    allSelected,
    onAdd,
    onAddAll,
    onRemove,
    disabled,
}: Readonly<{
    label: string;
    description: string;
    addLabel: string;
    allLabel: string;
    emptyText: string;
    options: readonly ComponentOption[];
    selectedIds: string[];
    allSelected: boolean;
    onAdd: (id: string) => void;
    onAddAll: () => void;
    onRemove: (id: string) => void;
    disabled?: boolean;
}>) {
    const [adding, setAdding] = useState(false);
    const [confirmAllOpen, setConfirmAllOpen] = useState(false);
    const nameOf = (id: string) => options.find(o => o.id === id)?.name ?? id;
    const descOf = (id: string) => options.find(o => o.id === id)?.description;

    const items: ComboBoxItem[] = [{ id: ALL_ID, label: allLabel, section: "default" }, ...options.filter(o => !selectedIds.includes(o.id)).map(o => ({ id: o.id, label: o.name, subtext: o.description, section: "advanced" as const }))];

    const handleSelect = (id: string) => {
        if (!id) return;
        setAdding(false);
        if (id === ALL_ID) {
            if (selectedIds.length > 0) {
                setConfirmAllOpen(true);
            } else {
                onAddAll();
            }
            return;
        }
        onAdd(id);
    };

    return (
        <section className="flex flex-col gap-3">
            <div className="flex flex-col gap-1">
                <div className="flex items-center justify-between gap-4">
                    <h3 className="h-8 font-semibold">{`${label} (${allSelected ? "All" : selectedIds.length})`}</h3>
                    {!disabled && !adding && !allSelected && (
                        <Button variant="ghost" size="sm" onClick={() => setAdding(true)}>
                            <Plus className="size-4" />
                            {addLabel}
                        </Button>
                    )}
                </div>
                <p className="text-(--secondary-text-wMain)">{description}</p>
            </div>

            {allSelected ? (
                <ul className="flex flex-col gap-2">
                    <RemovableRow label={allLabel} onRemove={() => onRemove(ALL_ID)} disabled={disabled} />
                </ul>
            ) : (
                <>
                    {adding && (
                        <div className="flex items-start gap-2">
                            <div className="flex-1">
                                <ComboBox defaultOpen items={items} value="" onValueChange={handleSelect} placeholder={addLabel} />
                            </div>
                            <Button variant="ghost" size="icon" tooltip="Cancel" onClick={() => setAdding(false)}>
                                <X />
                            </Button>
                        </div>
                    )}
                    {selectedIds.length > 0 && (
                        <ul className="flex flex-col gap-2">
                            {selectedIds.map(id => (
                                <RemovableRow key={id} label={nameOf(id)} description={descOf(id)} onRemove={() => onRemove(id)} disabled={disabled} />
                            ))}
                        </ul>
                    )}
                    {!adding && selectedIds.length === 0 && <div className="rounded-sm bg-(--secondary-w10) p-8 text-center text-(--secondary-text-wMain)">{emptyText}</div>}
                </>
            )}

            <ConfirmationDialog
                open={confirmAllOpen}
                onOpenChange={setConfirmAllOpen}
                title={`Switch to ${allLabel}?`}
                description={`Switching replaces the individually added ${label.toLowerCase()} with ${allLabel}.`}
                actionLabels={{ confirm: "Replace" }}
                onConfirm={onAddAll}
            />
        </section>
    );
}

function GrantSection({
    label,
    description,
    addLabel,
    allLabel,
    emptyText,
    options,
    value,
    kind,
    onChange,
    disabled,
}: Readonly<{
    label: string;
    description: string;
    addLabel: string;
    allLabel: string;
    emptyText: string;
    options: readonly ComponentOption[];
    value: string[];
    kind: ComponentKind;
    onChange: (scopes: string[]) => void;
    disabled?: boolean;
}>) {
    const [adding, setAdding] = useState(false);
    const [confirmAllOpen, setConfirmAllOpen] = useState(false);
    const ids = cardIds(value, kind);
    const all = hasAllCard(value, kind);
    const showTraces = kind === "agent";
    const nameOf = (id: string) => options.find(o => o.id === id)?.name ?? id;
    const descOf = (id: string) => options.find(o => o.id === id)?.description;

    const items: ComboBoxItem[] = [{ id: ALL_ID, label: allLabel, section: "default" }, ...options.filter(o => !ids.includes(o.id)).map(o => ({ id: o.id, label: o.name, subtext: o.description, section: "advanced" as const }))];

    const handleSelect = (id: string) => {
        if (!id) return;
        setAdding(false);
        if (id === ALL_ID) {
            if (ids.length > 0) {
                setConfirmAllOpen(true);
            } else {
                onChange(addAllCard(value, kind));
            }
            return;
        }
        onChange(addCard(value, kind, id));
    };
    const onGrant = (id: string) => (grant: CardGrant, on: boolean) => onChange(setCardGrant(value, kind, id, grant, on));

    return (
        <section className="flex flex-col gap-3">
            <div className="flex flex-col gap-1">
                <div className="flex items-center justify-between gap-4">
                    <h3 className="h-8 font-semibold">{`${label} (${all ? "All" : ids.length})`}</h3>
                    {!disabled && !adding && !all && (
                        <Button variant="ghost" size="sm" onClick={() => setAdding(true)}>
                            <Plus className="size-4" />
                            {addLabel}
                        </Button>
                    )}
                </div>
                <p className="text-(--secondary-text-wMain)">{description}</p>
            </div>

            {all ? (
                <ul className="flex flex-col gap-2">
                    <GrantCard label={allLabel} allOf={{ noun: kind }} showTraces={showTraces} grants={cardGrants(value, kind, ALL_ID)} onGrantChange={onGrant(ALL_ID)} onRemove={() => onChange(removeCard(value, kind, ALL_ID))} disabled={disabled} />
                </ul>
            ) : (
                <>
                    {adding && (
                        <div className="flex items-start gap-2">
                            <div className="flex-1">
                                <ComboBox defaultOpen items={items} value="" onValueChange={handleSelect} placeholder={addLabel} />
                            </div>
                            <Button variant="ghost" size="icon" tooltip="Cancel" onClick={() => setAdding(false)}>
                                <X />
                            </Button>
                        </div>
                    )}
                    {ids.length > 0 && (
                        <ul className="flex flex-col gap-2">
                            {ids.map(id => (
                                <GrantCard
                                    key={id}
                                    label={nameOf(id)}
                                    description={descOf(id)}
                                    showTraces={showTraces}
                                    grants={cardGrants(value, kind, id)}
                                    onGrantChange={onGrant(id)}
                                    onRemove={() => onChange(removeCard(value, kind, id))}
                                    disabled={disabled}
                                />
                            ))}
                        </ul>
                    )}
                    {!adding && ids.length === 0 && <div className="rounded-sm bg-(--secondary-w10) p-8 text-center text-(--secondary-text-wMain)">{emptyText}</div>}
                </>
            )}

            <ConfirmationDialog
                open={confirmAllOpen}
                onOpenChange={setConfirmAllOpen}
                title={`Switch to ${allLabel}?`}
                description={`Switching replaces the individually added ${label.toLowerCase()} with ${allLabel}, including any feedback or trace grants they held.`}
                actionLabels={{ confirm: "Replace" }}
                onConfirm={() => onChange(addAllCard(value, kind))}
            />
        </section>
    );
}

function AccessTab({ value, onChange, agents, workflows, disabled }: Readonly<{ value: string[]; onChange: (scopes: string[]) => void; agents: readonly ComponentOption[]; workflows: readonly ComponentOption[]; disabled?: boolean }>) {
    const feedbackGrants = useIsFeedbackManagementEnabled();

    const agentCopy = {
        label: "Agents",
        description: "Users with access to an agent can view and use its skills, tools, connectors, and models in the context of that agent.",
        addLabel: "Add Agent",
        allLabel: "All Agents",
        emptyText: "No agents added",
    };
    const workflowCopy = {
        label: "Workflows",
        description: "Users with access to a workflow can view and use its agents in the context of that workflow.",
        addLabel: "Add Workflow",
        allLabel: "All Workflows",
        emptyText: "No workflows added",
    };

    if (feedbackGrants) {
        return (
            <div className="flex flex-col gap-8">
                <GrantSection {...agentCopy} options={agents} value={value} kind="agent" onChange={onChange} disabled={disabled} />
                <GrantSection {...workflowCopy} options={workflows} value={value} kind="workflow" onChange={onChange} disabled={disabled} />
            </div>
        );
    }

    const section = (kind: ComponentKind) => ({
        selectedIds: componentIds(value, kind),
        allSelected: hasAllComponents(value, kind),
        onAdd: (id: string) => onChange(addComponent(value, kind, id)),
        onAddAll: () => onChange(grantAllComponents(value, kind)),
        onRemove: (id: string) => onChange(removeComponent(value, kind, id)),
    });

    return (
        <div className="flex flex-col gap-8">
            <ComponentSection {...agentCopy} options={agents} disabled={disabled} {...section("agent")} />
            <ComponentSection {...workflowCopy} options={workflows} disabled={disabled} {...section("workflow")} />
        </div>
    );
}

function AdvancedTab({ value, disabled }: Readonly<{ value: string[]; disabled?: boolean }>) {
    if (value.length === 0) {
        return <div className="rounded-sm bg-(--secondary-w10) p-8 text-center text-(--secondary-text-wMain)">{disabled ? "No scopes granted." : "No scopes granted. Add agents or workflows in the Access tab to grant scopes with this role."}</div>;
    }
    return (
        <div className="flex flex-col gap-3">
            <p className="mb-2">The complete set of scopes this role grants.</p>
            <ul className="flex flex-col gap-1">
                {[...value]
                    .sort((a, b) => a.localeCompare(b))
                    .map(scope => (
                        <li key={scope} className="rounded-sm bg-(--secondary-w10) px-3 py-1.5 font-mono text-xs">
                            {scope}
                        </li>
                    ))}
            </ul>
        </div>
    );
}

export function ScopeBundleEditor({ value, onChange, agents = [], workflows = [], disabled = false }: Readonly<ScopeBundleEditorProps>) {
    return (
        <Tabs variant="underline" defaultValue="access" className="w-full">
            <TabsList className="shrink-0">
                <TabsTrigger value="access" title="Access">
                    Access
                </TabsTrigger>
                <TabsTrigger value="scopes" title="Scopes">
                    Scopes
                </TabsTrigger>
            </TabsList>
            <TabsContent value="access" className="pt-6">
                <AccessTab value={value} onChange={onChange} agents={agents} workflows={workflows} disabled={disabled} />
            </TabsContent>
            <TabsContent value="scopes" className="pt-6">
                <AdvancedTab value={value} disabled={disabled} />
            </TabsContent>
        </Tabs>
    );
}
