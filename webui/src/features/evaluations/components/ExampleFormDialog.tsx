// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";

import { PageLabel, PageLabelWithValue } from "@/common/layout";

import { Button } from "@/ui/button";
import { Dialog, DialogClose, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";
import { Textarea } from "@/ui/textarea";
import type { CreateExampleRequest, EvalExample } from "@/types/eval";

interface ExampleFormDialogProps {
    open: boolean;
    onClose: () => void;
    onSave: (data: CreateExampleRequest) => Promise<void>;
    example?: EvalExample | null;
}

type FormState = {
    prompt: string;
    expectedResponse: string;
};

const DEFAULT_FORM: FormState = {
    prompt: "",
    expectedResponse: "",
};

export function ExampleFormDialog({ open, onClose, onSave, example }: ExampleFormDialogProps) {
    const [form, setForm] = useState<FormState>(DEFAULT_FORM);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState<string | null>(null);

    useEffect(() => {
        if (!open) return;
        if (example) {
            setForm({
                prompt: example.prompt,
                expectedResponse: example.expectedResponse ?? "",
            });
        } else {
            setForm(DEFAULT_FORM);
        }
        setError(null);
    }, [open, example]);

    const set = (field: keyof FormState) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => {
        setForm(prev => ({ ...prev, [field]: e.target.value }));
    };

    const handleSave = async () => {
        if (!form.prompt.trim()) {
            setError("Prompt is required");
            return;
        }
        setSaving(true);
        setError(null);
        try {
            await onSave({
                prompt: form.prompt.trim(),
                expectedResponse: form.expectedResponse.trim() || undefined,
            });
            onClose();
        } catch (err) {
            setError(String(err));
        } finally {
            setSaving(false);
        }
    };

    return (
        <Dialog open={open} onOpenChange={v => !v && onClose()}>
            <DialogContent className="max-w-2xl">
                <DialogHeader>
                    <DialogTitle>{example ? "Edit Example" : "New Example"}</DialogTitle>
                </DialogHeader>
                <div className="flex max-h-[70vh] flex-col gap-4 overflow-y-auto py-2">
                    <PageLabelWithValue>
                        <PageLabel required>Prompt</PageLabel>
                        <Textarea value={form.prompt} onChange={set("prompt")} placeholder="The user message to send to the agent" rows={3} disabled={saving} />
                    </PageLabelWithValue>

                    <PageLabelWithValue>
                        <PageLabel>Expected Response</PageLabel>
                        <Textarea value={form.expectedResponse} onChange={set("expectedResponse")} placeholder="The ideal agent response (optional)" rows={3} disabled={saving} />
                    </PageLabelWithValue>

                    {error && <span className="text-xs text-(--error-wMain)">{error}</span>}
                </div>
                <DialogFooter>
                    <DialogClose asChild>
                        <Button variant="outline" disabled={saving}>
                            Cancel
                        </Button>
                    </DialogClose>
                    <Button onClick={handleSave} disabled={saving}>
                        {saving ? "Saving..." : "Save"}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
