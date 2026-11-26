// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useCallback, useEffect, useMemo, useState } from "react";
import { Plus, X } from "lucide-react";

import { MessageBanner } from "@/common/banners/MessageBanner";
import { UserMultiTypeahead } from "@/common/form/UserMultiTypeahead";

import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Spinner } from "@/ui";

export interface ShareResourceEntry<TAccess extends string = string> {
    id: string;
    userEmail: string;
    accessLevel: TAccess;
}

export interface AccessLevelOption<TAccess extends string = string> {
    value: TAccess;
    label: string;
    icon?: ReactNode;
    description?: string;
}

export interface ShareSaveSummary {
    added: string[];
    removed: string[];
    updated: string[];
}

export type ShareRowColumnPosition = "before-access" | "after-access";

export interface ShareRowColumn<TAccess extends string = string> {
    key: string;
    header: ReactNode;
    width?: string;
    position: ShareRowColumnPosition;
    render: (share: ShareResourceEntry<TAccess>) => ReactNode;
}

export interface ShareResourceDialogProps<TAccess extends string = string> {
    isOpen: boolean;
    onClose: () => void;
    resourceLabel: string;
    resourceName: string;
    ownerEmail?: string;
    shares: ShareResourceEntry<TAccess>[];
    isLoading?: boolean;
    identityServiceType?: string | null;
    accessLevels: AccessLevelOption<TAccess>[];
    onCreate?: (entries: Array<{ userEmail: string; accessLevel: TAccess }>) => Promise<void> | void;
    onReplace?: (entries: Array<{ userEmail: string; accessLevel: TAccess }>) => Promise<void> | void;
    onSuccess?: (summary: ShareSaveSummary) => void;
    extraHasChanges?: boolean;
    onSave?: () => Promise<void> | void;
    description?: ReactNode;
    titleAccessory?: ReactNode;
    extraColumns?: ShareRowColumn<TAccess>[];
    belowSharesSlot?: ReactNode;
    footerStartSlot?: ReactNode;
    defaultOpenAddRow?: boolean;
}

interface AddGroup<TAccess extends string> {
    id: string;
    emails: string[];
    accessLevel: TAccess;
}

function PlainEmailInput({ onCommit, autoFocus = false }: Readonly<{ onCommit: (email: string) => void; autoFocus?: boolean }>) {
    const flush = (el: HTMLInputElement) => {
        const v = el.value.trim();
        if (v) {
            onCommit(v);
            el.value = "";
        }
    };
    return (
        <Input
            type="email"
            autoComplete="email"
            autoFocus={autoFocus}
            placeholder="Enter email address and press Enter..."
            onKeyDown={e => {
                if (e.key === "Enter") {
                    e.preventDefault();
                    flush(e.currentTarget);
                }
            }}
            onBlur={e => flush(e.currentTarget)}
        />
    );
}

export function ShareResourceDialog<TAccess extends string = string>({
    isOpen,
    onClose,
    resourceLabel,
    resourceName,
    ownerEmail,
    shares,
    isLoading = false,
    identityServiceType = null,
    accessLevels,
    onCreate,
    onReplace,
    onSuccess,
    extraHasChanges = false,
    onSave,
    description,
    titleAccessory,
    extraColumns,
    belowSharesSlot,
    footerStartSlot,
    defaultOpenAddRow = false,
}: Readonly<ShareResourceDialogProps<TAccess>>) {
    const defaultLevel = accessLevels[0]?.value;

    const renderAccessLevelSelect = (value: TAccess | undefined, onChange: (v: TAccess) => void) => {
        const selected = accessLevels.find(o => o.value === value);
        return (
            <Select value={value} onValueChange={v => onChange(v as TAccess)}>
                <SelectTrigger className="w-full">
                    <SelectValue>{selected?.label}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                    {accessLevels.map(opt => (
                        <SelectItem key={opt.value} value={opt.value}>
                            <div className="flex items-center gap-4">
                                {opt.icon && <span className="flex flex-shrink-0 items-center justify-center">{opt.icon}</span>}
                                <span className="flex flex-col">
                                    <span>{opt.label}</span>
                                    {opt.description && <span className="text-xs text-(--secondary-text-wMain)">{opt.description}</span>}
                                </span>
                            </div>
                        </SelectItem>
                    ))}
                </SelectContent>
            </Select>
        );
    };

    const [addGroups, setAddGroups] = useState<AddGroup<TAccess>[]>([]);
    const [pendingRemoves, setPendingRemoves] = useState<string[]>([]);
    const [editedLevels, setEditedLevels] = useState<Record<string, TAccess>>({});
    const [isSaving, setIsSaving] = useState(false);
    const [error, setError] = useState<string | null>(null);

    useEffect(() => {
        if (isOpen) {
            const initialAdd: AddGroup<TAccess>[] = defaultOpenAddRow && defaultLevel !== undefined ? [{ id: crypto.randomUUID(), emails: [], accessLevel: defaultLevel }] : [];
            setAddGroups(initialAdd);
            setPendingRemoves([]);
            setEditedLevels({});
            setError(null);
        }
    }, [isOpen, defaultOpenAddRow, defaultLevel]);

    const columnsBeforeAccess = useMemo(() => (extraColumns ?? []).filter(c => c.position === "before-access"), [extraColumns]);
    const columnsAfterAccess = useMemo(() => (extraColumns ?? []).filter(c => c.position === "after-access"), [extraColumns]);
    const existingRowGridCols = useMemo(() => {
        const trackOf = (c: ShareRowColumn<TAccess>) => c.width ?? "auto";
        return ["2fr", ...columnsBeforeAccess.map(trackOf), "1fr", ...columnsAfterAccess.map(trackOf), "32px"].join(" ");
    }, [columnsBeforeAccess, columnsAfterAccess]);

    const completedAdds = useMemo(() => addGroups.flatMap(g => g.emails.map(email => ({ userEmail: email, accessLevel: g.accessLevel }))), [addGroups]);
    const hasRemoves = pendingRemoves.length > 0;
    const realEditedEmails = useMemo(
        () =>
            Object.keys(editedLevels).filter(email => {
                const original = shares.find(s => s.userEmail === email)?.accessLevel;
                return original !== undefined && editedLevels[email] !== original;
            }),
        [editedLevels, shares]
    );
    const hasEdits = realEditedEmails.length > 0;
    const hasChanges = completedAdds.length > 0 || hasRemoves || hasEdits || extraHasChanges;

    const visibleShares = shares.filter(s => !pendingRemoves.includes(s.userEmail)).sort((a, b) => a.userEmail.localeCompare(b.userEmail, undefined, { sensitivity: "base" }));
    const existingEmails = useMemo(() => shares.filter(s => !pendingRemoves.includes(s.userEmail)).map(s => s.userEmail), [shares, pendingRemoves]);
    const ownerExclusion = useMemo(() => (ownerEmail ? [ownerEmail] : []), [ownerEmail]);
    const pendingEmails = completedAdds.map(a => a.userEmail);
    const excludeFromTypeahead = [...ownerExclusion, ...existingEmails, ...pendingEmails];

    const handleAddRow = useCallback(() => {
        setAddGroups(prev => [{ id: crypto.randomUUID(), emails: [], accessLevel: defaultLevel }, ...prev]);
    }, [defaultLevel]);

    const handleRemoveRow = useCallback((groupId: string) => {
        setAddGroups(prev => prev.filter(g => g.id !== groupId));
    }, []);

    const handleAddEmail = useCallback((groupId: string, email: string) => {
        setAddGroups(prev => {
            if (prev.some(g => g.emails.includes(email))) return prev;
            const addToGroup = (g: AddGroup<TAccess>): AddGroup<TAccess> => (g.id === groupId ? { ...g, emails: [...g.emails, email] } : g);
            return prev.map(addToGroup);
        });
    }, []);

    const handleRemoveEmail = useCallback((groupId: string, email: string) => {
        const removeFromGroup = (g: AddGroup<TAccess>): AddGroup<TAccess> => (g.id === groupId ? { ...g, emails: g.emails.filter(e => e !== email) } : g);
        setAddGroups(prev => prev.map(removeFromGroup));
    }, []);

    const handleSetGroupLevel = useCallback((groupId: string, level: TAccess) => {
        const setLevel = (g: AddGroup<TAccess>): AddGroup<TAccess> => (g.id === groupId ? { ...g, accessLevel: level } : g);
        setAddGroups(prev => prev.map(setLevel));
    }, []);

    const handleRemoveExisting = useCallback((email: string) => {
        setPendingRemoves(prev => (prev.includes(email) ? prev : [...prev, email]));
    }, []);

    const handleEditLevel = useCallback((email: string, level: TAccess) => {
        setEditedLevels(prev => ({ ...prev, [email]: level }));
    }, []);

    const handleDiscard = useCallback(() => {
        setAddGroups([]);
        setPendingRemoves([]);
        setEditedLevels({});
        setError(null);
        onClose();
    }, [onClose]);

    const handleSave = useCallback(async () => {
        setError(null);
        setIsSaving(true);
        try {
            const existingTouched = hasRemoves || hasEdits;
            let didCall = false;
            if (existingTouched && onReplace) {
                const remaining = shares.filter(s => !pendingRemoves.includes(s.userEmail)).map(s => ({ userEmail: s.userEmail, accessLevel: editedLevels[s.userEmail] ?? s.accessLevel }));
                await onReplace([...remaining, ...completedAdds]);
                didCall = true;
            } else if (!existingTouched && completedAdds.length > 0 && onCreate) {
                await onCreate(completedAdds);
                didCall = true;
            }
            if (didCall) {
                onSuccess?.({
                    added: completedAdds.map(a => a.userEmail),
                    removed: [...pendingRemoves],
                    updated: realEditedEmails.filter(email => !pendingRemoves.includes(email)),
                });
            }
            await onSave?.();
            onClose();
        } catch (e) {
            setError(e instanceof Error ? e.message : "Failed to save changes");
        } finally {
            setIsSaving(false);
        }
    }, [hasRemoves, hasEdits, shares, pendingRemoves, editedLevels, completedAdds, realEditedEmails, onReplace, onCreate, onSave, onClose, onSuccess]);

    const hasEmptyState = !isLoading && visibleShares.length === 0 && addGroups.length === 0;

    const renderBody = () => {
        if (isLoading) {
            return (
                <output className="flex items-center justify-center py-8">
                    <Spinner size="medium" variant="muted" />
                </output>
            );
        }
        if (hasEmptyState && !ownerEmail) {
            return <div className="py-8 text-center text-sm text-(--secondary-text-wMain)">No users have access to this {resourceLabel.toLowerCase()} yet.</div>;
        }
        return (
            <div className="flex flex-col gap-2">
                {addGroups.map((group, idx) => (
                    <div key={group.id} data-testid="pending-share-row" className="grid items-start gap-2 py-1" style={{ gridTemplateColumns: existingRowGridCols }}>
                        {identityServiceType === null ? (
                            <PlainEmailInput onCommit={email => handleAddEmail(group.id, email)} autoFocus={idx === 0} />
                        ) : (
                            <UserMultiTypeahead
                                id={group.id}
                                selectedEmails={group.emails}
                                onAdd={email => handleAddEmail(group.id, email)}
                                onRemove={email => handleRemoveEmail(group.id, email)}
                                excludeEmails={excludeFromTypeahead}
                                autoFocus={idx === 0}
                            />
                        )}
                        {columnsBeforeAccess.map(c => (
                            <span key={c.key} />
                        ))}
                        {renderAccessLevelSelect(group.accessLevel, level => handleSetGroupLevel(group.id, level))}
                        {columnsAfterAccess.map(c => (
                            <span key={c.key} />
                        ))}
                        <Button variant="ghost" size="sm" onClick={() => handleRemoveRow(group.id)} aria-label="Remove pending share row">
                            <X className="h-4 w-4" />
                        </Button>
                    </div>
                ))}

                {(ownerEmail || visibleShares.length > 0) && (
                    <div className="mt-2">
                        <div className="grid items-center gap-2 px-3 pb-2 text-xs font-medium text-(--secondary-text-wMain)" style={{ gridTemplateColumns: existingRowGridCols }}>
                            <span>Email</span>
                            {columnsBeforeAccess.map(c => (
                                <span key={c.key}>{c.header}</span>
                            ))}
                            <span>Access Level</span>
                            {columnsAfterAccess.map(c => (
                                <span key={c.key}>{c.header}</span>
                            ))}
                            <div />
                        </div>
                        <div className="divide-y divide-(--secondary-w40) rounded-md border border-(--secondary-w40)">
                            {ownerEmail && (
                                <div data-testid="owner-row" className="grid items-center gap-2 px-3 py-3" style={{ gridTemplateColumns: existingRowGridCols }}>
                                    <span className="truncate text-sm" title={ownerEmail}>
                                        {ownerEmail}
                                    </span>
                                    {columnsBeforeAccess.map(c => (
                                        <span key={c.key} />
                                    ))}
                                    <span className="text-sm text-(--secondary-text-wMain)">Owner</span>
                                    {columnsAfterAccess.map(c => (
                                        <span key={c.key} />
                                    ))}
                                    <div />
                                </div>
                            )}
                            {visibleShares.map(share => {
                                const currentLevel = editedLevels[share.userEmail] ?? share.accessLevel;
                                return (
                                    <div key={share.id} data-testid="share-row" className="grid items-center gap-2 px-3 py-3" style={{ gridTemplateColumns: existingRowGridCols }}>
                                        <span className="truncate text-sm" title={share.userEmail}>
                                            {share.userEmail}
                                        </span>
                                        {columnsBeforeAccess.map(c => (
                                            <div key={c.key}>{c.render(share)}</div>
                                        ))}
                                        {renderAccessLevelSelect(currentLevel, value => handleEditLevel(share.userEmail, value))}
                                        {columnsAfterAccess.map(c => (
                                            <div key={c.key}>{c.render(share)}</div>
                                        ))}
                                        <Button variant="ghost" size="sm" data-testid="remove-share" disabled={isSaving} onClick={() => handleRemoveExisting(share.userEmail)} aria-label={`Remove ${share.userEmail}`}>
                                            <X className="h-4 w-4" />
                                        </Button>
                                    </div>
                                );
                            })}
                        </div>
                    </div>
                )}
            </div>
        );
    };

    const defaultDescription = (
        <>
            Share <strong>{resourceName}</strong> with other users so they can access and use it directly.
        </>
    );

    return (
        <Dialog open={isOpen} onOpenChange={open => !open && handleDiscard()}>
            <DialogContent className="flex max-h-[80vh] flex-col overflow-hidden sm:max-w-3xl">
                <DialogHeader className="mb-6 flex-shrink-0">
                    <div className="flex items-center gap-2">
                        <DialogTitle>Share {resourceLabel}</DialogTitle>
                        {titleAccessory}
                    </div>
                    <div className="flex items-center justify-between gap-4">
                        <DialogDescription>{description ?? defaultDescription}</DialogDescription>
                        <Button variant="outline" size="default" onClick={handleAddRow} disabled={isSaving || addGroups.some(g => g.emails.length === 0)} className="gap-1">
                            <Plus className="h-4 w-4" />
                            Add
                        </Button>
                    </div>
                </DialogHeader>

                {error && (
                    <div className="flex-shrink-0 py-2">
                        <MessageBanner variant="error" message={error} />
                    </div>
                )}

                <div className="min-h-0 flex-1 overflow-y-auto">
                    {renderBody()}
                    {belowSharesSlot}
                </div>

                <DialogFooter className="flex-shrink-0">
                    {footerStartSlot}
                    {footerStartSlot && <div className="flex-1" />}
                    <Button variant="ghost" onClick={handleDiscard} disabled={isSaving}>
                        Discard Changes
                    </Button>
                    <Button onClick={handleSave} disabled={isSaving || !hasChanges}>
                        {isSaving && <Spinner size="small" className="mr-2" />}
                        Save
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
