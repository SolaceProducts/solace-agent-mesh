// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";
import { Sparkles } from "lucide-react";

import { evaluationsApi } from "@/api/evaluations";

import { PageLabel, PageLabelWithValue } from "@/common/layout";

import { getErrorMessage } from "@/utils/api";

import { Button } from "@/ui/button";
import { Dialog, DialogClose, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";
import { Input } from "@/ui/input";
import { Textarea } from "@/ui/textarea";
import type { EvalDataset } from "@/types/eval";

import { useAgentOptions } from "@/features/entrypoints/hooks";

import { MessageBanner } from "@/common";

import { MAX_EXAMPLES_PER_DATASET } from "../constants";

import { AgentAutocomplete } from "./AgentAutocomplete";

const EXAMPLES_PER_CALL_CAP = 20;

type DatasetMode = {
    readonly mode: "dataset";
    readonly onGenerated: (dataset: EvalDataset) => void;
};

type ExamplesMode = {
    readonly mode: "examples";
    readonly datasetId: string;
    readonly existingCount: number;
    readonly onGenerated: () => void;
};

type AiGenerationDialogProps = {
    readonly open: boolean;
    readonly onClose: () => void;
    readonly defaultTargetAgent?: string;
} & (DatasetMode | ExamplesMode);

export function AiGenerationDialog(props: AiGenerationDialogProps) {
    const { open, onClose, defaultTargetAgent } = props;
    const agentOptions = useAgentOptions();

    const isExamplesMode = props.mode === "examples";
    const existingCount = isExamplesMode ? props.existingCount : 0;
    const remaining = isExamplesMode ? Math.max(0, MAX_EXAMPLES_PER_DATASET - existingCount) : MAX_EXAMPLES_PER_DATASET;
    const maxThisCall = isExamplesMode ? Math.min(EXAMPLES_PER_CALL_CAP, remaining) : MAX_EXAMPLES_PER_DATASET;
    const initialCount = isExamplesMode ? Math.min(3, maxThisCall) || 1 : 10;

    const [targetAgent, setTargetAgent] = useState(defaultTargetAgent ?? "");
    const [numExamples, setNumExamples] = useState<number | "">(initialCount);
    const numExamplesValue = typeof numExamples === "number" ? numExamples : 0;
    const [instructions, setInstructions] = useState("");
    const [generating, setGenerating] = useState(false);
    const [error, setError] = useState<string | null>(null);

    useEffect(() => {
        if (defaultTargetAgent) setTargetAgent(defaultTargetAgent);
    }, [defaultTargetAgent]);

    useEffect(() => {
        if (open) {
            setNumExamples(initialCount);
            setInstructions("");
            setError(null);
        }
    }, [open, initialCount]);

    const reset = () => {
        setTargetAgent(defaultTargetAgent ?? "");
        setNumExamples(initialCount);
        setInstructions("");
        setError(null);
    };

    const handleClose = () => {
        if (!generating) {
            reset();
            onClose();
        }
    };

    const handleGenerate = async () => {
        if (!targetAgent || numExamplesValue < 1) return;
        setGenerating(true);
        setError(null);
        try {
            const focusTrimmed = instructions.trim() || undefined;
            if (props.mode === "dataset") {
                const dataset = await evaluationsApi.generateDataset({
                    targetAgent,
                    numExamples: numExamplesValue,
                    focus: focusTrimmed,
                });
                reset();
                props.onGenerated(dataset);
            } else {
                await evaluationsApi.generateExamples(props.datasetId, {
                    targetAgent,
                    numExamples: numExamplesValue,
                    focus: focusTrimmed,
                });
                reset();
                props.onGenerated();
            }
            onClose();
        } catch (err) {
            setError(getErrorMessage(err));
        } finally {
            setGenerating(false);
        }
    };

    const isDisabled = generating || !targetAgent || numExamplesValue < 1 || (isExamplesMode && maxThisCall < 1);

    const title = isExamplesMode ? "Generate Examples with AI" : "Generate Dataset with AI";
    const description = isExamplesMode
        ? "Pick an agent and how many examples to generate. The AI will craft realistic prompts tailored to that agent, avoid duplicating your existing examples, and include an expected response only when there's a verifiable correct answer."
        : "Pick an agent and how many examples you want. The AI will generate realistic prompts and expected responses tailored to that agent, and choose a suitable name and description for the dataset.";
    const agentHelper = isExamplesMode ? "The agent's name, description, and instructions will be sent to the AI so it can generate relevant examples." : "The agent's name, description, and instructions will be included in the generation request.";
    const countHelper = isExamplesMode
        ? maxThisCall < 1
            ? `Dataset is full (${MAX_EXAMPLES_PER_DATASET} examples max).`
            : `You can add up to ${maxThisCall} more (per-call cap ${EXAMPLES_PER_CALL_CAP}; dataset cap ${MAX_EXAMPLES_PER_DATASET}).`
        : `Between 1 and ${MAX_EXAMPLES_PER_DATASET}.`;
    const generatingMessage = isExamplesMode
        ? "Generating examples… This may take up to a minute while the AI crafts examples tailored to your agent."
        : "Generating your dataset… This may take up to a minute while the AI crafts examples for your agent.";

    const countMin = 1;
    const countMax = isExamplesMode ? maxThisCall || 1 : MAX_EXAMPLES_PER_DATASET;
    const countDisabled = generating || (isExamplesMode && maxThisCall < 1);

    return (
        <Dialog
            open={open}
            onOpenChange={v => {
                if (!v) handleClose();
            }}
        >
            <DialogContent className="sm:max-w-[600px]">
                <DialogHeader>
                    <DialogTitle className="flex items-center gap-2">
                        <Sparkles className="size-4 text-(--primary-wMain)" />
                        {title}
                    </DialogTitle>
                </DialogHeader>

                <div className="flex flex-col gap-4 py-2">
                    <p className="text-sm text-(--secondary-text-wMain)">{description}</p>

                    <PageLabelWithValue>
                        <PageLabel required>Agent</PageLabel>
                        <AgentAutocomplete options={agentOptions} value={targetAgent} onChange={setTargetAgent} disabled={generating} />
                        <span className="text-xs text-(--secondary-text-wMain)">{agentHelper}</span>
                    </PageLabelWithValue>

                    <PageLabelWithValue>
                        <PageLabel required>Number of Examples</PageLabel>
                        <Input
                            type="text"
                            inputMode="numeric"
                            value={numExamples === "" ? "" : String(numExamples)}
                            onChange={e => {
                                const raw = e.target.value;
                                if (raw === "") {
                                    setNumExamples("");
                                    return;
                                }
                                const parsed = Number(raw);
                                if (Number.isInteger(parsed) && parsed >= 0) setNumExamples(parsed);
                            }}
                            onBlur={() => {
                                if (numExamples === "" || numExamples < countMin) setNumExamples(countMin);
                                else if (numExamples > countMax) setNumExamples(countMax);
                            }}
                            className="w-24"
                            disabled={countDisabled}
                        />
                        <span className="text-xs text-(--secondary-text-wMain)">{countHelper}</span>
                    </PageLabelWithValue>

                    <PageLabelWithValue>
                        <PageLabel>Additional Instructions (optional)</PageLabel>
                        <Textarea value={instructions} onChange={e => setInstructions(e.target.value)} placeholder="e.g. edge cases on refusals, multi-step reasoning, ambiguous inputs" rows={3} maxLength={500} disabled={generating} />
                        <span className="text-xs text-(--secondary-text-wMain)">Leave blank for a balanced mix. Fill this in to steer the generation toward a specific theme.</span>
                    </PageLabelWithValue>

                    {error && <MessageBanner variant="error" message={error} />}

                    {generating && <MessageBanner variant="info" message={generatingMessage} />}
                </div>

                <DialogFooter>
                    <DialogClose asChild>
                        <Button variant="ghost" disabled={generating}>
                            Cancel
                        </Button>
                    </DialogClose>
                    <Button onClick={() => void handleGenerate()} disabled={isDisabled} className="flex items-center gap-1.5">
                        <Sparkles className="size-3.5" />
                        {generating ? "Generating..." : "Generate"}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
