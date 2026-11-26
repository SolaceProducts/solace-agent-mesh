// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

import { Textarea } from "@/ui/textarea";

interface InlineEditableCellProps {
    readonly value: string;
    readonly onSave: (value: string) => Promise<void>;
    readonly placeholder?: string;
    readonly readOnly?: boolean;
    readonly keepDraftOnError?: boolean;
}

export function InlineEditableCell({ value, onSave, placeholder, readOnly, keepDraftOnError }: InlineEditableCellProps) {
    const [editing, setEditing] = useState(false);
    const [draft, setDraft] = useState(value);
    const [saving, setSaving] = useState(false);
    const textareaRef = useRef<HTMLTextAreaElement>(null);
    const savingRef = useRef(false);

    useEffect(() => {
        if (!editing && !savingRef.current) {
            setDraft(value);
        }
    }, [value, editing]);

    useEffect(() => {
        if (editing && textareaRef.current) {
            textareaRef.current.focus();
            textareaRef.current.selectionStart = textareaRef.current.value.length;
        }
    }, [editing]);

    const handleSave = useCallback(async () => {
        const trimmed = draft.trim();
        if (trimmed === value.trim()) {
            setEditing(false);
            return;
        }
        setSaving(true);
        savingRef.current = true;
        try {
            await onSave(trimmed);
            setEditing(false);
        } catch {
            if (!keepDraftOnError) {
                setDraft(value);
                setEditing(false);
            }
        } finally {
            setSaving(false);
            savingRef.current = false;
        }
    }, [draft, value, onSave, keepDraftOnError]);

    const handleKeyDown = (e: React.KeyboardEvent) => {
        if (e.key === "Escape") {
            setDraft(value);
            setEditing(false);
        }
        if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            void handleSave();
        }
    };

    if (readOnly) {
        return <div className="min-h-[32px] px-2 py-1.5 text-sm whitespace-pre-wrap">{value || <span className="text-(--secondary-text-wMain)">{placeholder ?? ""}</span>}</div>;
    }

    if (editing) {
        return <Textarea ref={textareaRef} value={draft} onChange={e => setDraft(e.target.value)} onBlur={() => void handleSave()} onKeyDown={handleKeyDown} disabled={saving} placeholder={placeholder} rows={3} className="min-h-[60px] text-sm" />;
    }

    return (
        <div
            className="min-h-[32px] cursor-text rounded-xs border border-transparent px-2 py-1.5 text-sm whitespace-pre-wrap transition-colors hover:border-(--secondary-w20)"
            onClick={() => setEditing(true)}
            onKeyDown={e => {
                if (e.key === "Enter" || e.key === " ") setEditing(true);
            }}
            role="button"
            tabIndex={0}
        >
            {value || <span className="text-(--secondary-text-wMain)">{placeholder ?? "Click to edit"}</span>}
        </div>
    );
}
