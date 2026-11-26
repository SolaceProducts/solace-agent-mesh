// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";
import { Pencil, Play } from "lucide-react";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/ui/tabs";
import { Textarea } from "@/ui/textarea";

export const TEST_PLAN_ARTIFACT_FILENAME = "test_plan.json";

export interface TestPlan {
    componentName: string;
    componentKind: "workflow" | "agent" | "skill";
    sampleInput: unknown;
    expectedOutput: unknown;
    rationale?: string;
}

interface TestPlanCardProps {
    plan: TestPlan;
    onStartTest: (plan: TestPlan) => void;
    onCancel: () => void;
    busy?: boolean;
    runNumber?: number | null;
}

function stringify(v: unknown): string {
    if (typeof v === "string") return v;
    try {
        return JSON.stringify(v, null, 2);
    } catch {
        return String(v);
    }
}

function parseField(raw: string): unknown {
    const trimmed = raw.trim();
    if (trimmed === "") return "";
    if (trimmed.startsWith("{") || trimmed.startsWith("[") || trimmed.startsWith('"')) {
        try {
            return JSON.parse(trimmed);
        } catch {
            throw new Error("Field looks like JSON but is not valid.");
        }
    }
    return raw;
}

export function TestPlanCard({ plan, onStartTest, onCancel, busy = false, runNumber = null }: TestPlanCardProps) {
    const [edited, setEdited] = useState<TestPlan>(() => plan);
    const [tab, setTab] = useState<"sampleInput" | "expectedOutput">("sampleInput");

    const [editOpen, setEditOpen] = useState(false);
    const [draftInputText, setDraftInputText] = useState("");
    const [draftOutputText, setDraftOutputText] = useState("");

    const sampleInputText = useMemo(() => stringify(edited.sampleInput), [edited.sampleInput]);
    const expectedOutputText = useMemo(() => stringify(edited.expectedOutput), [edited.expectedOutput]);

    const draft = useMemo<{ sampleInput?: unknown; expectedOutput?: unknown; error: string | null }>(() => {
        try {
            const sampleInput = parseField(draftInputText);
            try {
                const expectedOutput = parseField(draftOutputText);
                return { sampleInput, expectedOutput, error: null };
            } catch (err) {
                return { error: `Expected Output: ${(err as Error).message}` };
            }
        } catch (err) {
            return { error: `Sample Input: ${(err as Error).message}` };
        }
    }, [draftInputText, draftOutputText]);

    const openEdit = () => {
        setDraftInputText(sampleInputText);
        setDraftOutputText(expectedOutputText);
        setEditOpen(true);
    };

    const handleSave = () => {
        if (draft.error) return;
        setEdited(prev => ({ ...prev, sampleInput: draft.sampleInput, expectedOutput: draft.expectedOutput }));
        setEditOpen(false);
    };

    return (
        <div className="mt-3 flex flex-col gap-3 rounded-md border bg-(--background-w10) p-4">
            <div className="flex items-start justify-between gap-3">
                <div className="min-w-0 flex-1">
                    <h3 className="text-sm font-semibold">
                        {runNumber !== null && runNumber !== undefined ? `Test ${runNumber} Plan` : "Test Plan"}: <span className="font-normal">{plan.componentName}</span>
                    </h3>
                    <p className="mt-1 leading-relaxed">{plan.rationale ? plan.rationale : "The following test parameters have been generated based on your component's configuration. You can continue directly or make adjustments."}</p>
                </div>
                <Button variant="ghost" size="sm" className="h-7 w-7 shrink-0 p-0" tooltip="Edit" disabled={busy} onClick={openEdit}>
                    <Pencil className="h-3.5 w-3.5" />
                </Button>
            </div>

            <Tabs value={tab} onValueChange={v => setTab(v as "sampleInput" | "expectedOutput")}>
                <TabsList className="flex h-auto max-w-full overflow-x-auto rounded-md border bg-transparent p-0">
                    <TabsTrigger
                        value="sampleInput"
                        className="shrink-0 rounded-none border-0 border-l border-(--secondary-w20) px-4 py-1.5 text-xs whitespace-nowrap text-(--secondary-text-wMain) first:border-l-0 hover:bg-(--secondary-w10) data-[state=active]:bg-(--primary-w10) data-[state=active]:font-semibold data-[state=active]:text-(--primary-text-wMain) data-[state=active]:shadow-none"
                    >
                        Sample Input
                    </TabsTrigger>
                    <TabsTrigger
                        value="expectedOutput"
                        className="shrink-0 rounded-none border-0 border-l border-(--secondary-w20) px-4 py-1.5 text-xs whitespace-nowrap text-(--secondary-text-wMain) first:border-l-0 hover:bg-(--secondary-w10) data-[state=active]:bg-(--primary-w10) data-[state=active]:font-semibold data-[state=active]:text-(--primary-text-wMain) data-[state=active]:shadow-none"
                    >
                        Expected Output
                    </TabsTrigger>
                </TabsList>

                <TabsContent value="sampleInput" className="m-0 mt-2">
                    <FieldView text={sampleInputText} />
                </TabsContent>
                <TabsContent value="expectedOutput" className="m-0 mt-2">
                    <FieldView text={expectedOutputText} />
                </TabsContent>
            </Tabs>

            <div className="flex justify-end gap-2">
                <Button size="sm" variant="ghost" onClick={onCancel} disabled={busy}>
                    Cancel
                </Button>
                <Button size="sm" onClick={() => onStartTest(edited)} disabled={busy} className="gap-1.5">
                    <Play className="h-3.5 w-3.5" />
                    Start Test
                </Button>
            </div>

            <Dialog open={editOpen} onOpenChange={setEditOpen}>
                <DialogContent className="flex max-h-[80vh] flex-col sm:max-w-3xl">
                    <DialogHeader>
                        <DialogTitle>Edit Test Plan</DialogTitle>
                        <DialogDescription>Adjust the sample input and expected output for this run. JSON objects and plain text are both accepted.</DialogDescription>
                    </DialogHeader>
                    <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-auto pt-2">
                        <div className="flex flex-col gap-1.5">
                            <label htmlFor="test-plan-sample-input" className="text-xs font-semibold">
                                Sample Input
                            </label>
                            <Textarea
                                id="test-plan-sample-input"
                                className="min-h-32 w-full resize-y rounded-md border bg-(--background-w20) p-2 font-mono text-xs"
                                value={draftInputText}
                                onChange={e => setDraftInputText(e.target.value)}
                                spellCheck={false}
                                autoFocus
                            />
                        </div>
                        <div className="flex flex-col gap-1.5">
                            <label htmlFor="test-plan-expected-output" className="text-xs font-semibold">
                                Expected Output
                            </label>
                            <Textarea
                                id="test-plan-expected-output"
                                className="min-h-32 w-full resize-y rounded-md border bg-(--background-w20) p-2 font-mono text-xs"
                                value={draftOutputText}
                                onChange={e => setDraftOutputText(e.target.value)}
                                spellCheck={false}
                            />
                        </div>
                        {draft.error && <p className="text-xs text-(--error-wMain)">{draft.error}</p>}
                    </div>
                    <DialogFooter>
                        <Button variant="outline" onClick={() => setEditOpen(false)}>
                            Discard Changes
                        </Button>
                        <Button onClick={handleSave} disabled={!!draft.error}>
                            Save
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}

function FieldView({ text }: Readonly<{ text: string }>) {
    return <pre className="max-h-48 overflow-auto rounded-md border bg-(--background-w20) p-2 font-mono text-xs whitespace-pre-wrap">{text}</pre>;
}
