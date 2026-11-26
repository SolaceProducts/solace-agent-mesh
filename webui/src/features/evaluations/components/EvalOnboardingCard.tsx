// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ArrowRight, FlaskConical } from "lucide-react";

import { Button } from "@/ui/button";

interface EvalOnboardingCardProps {
    readonly heading?: string;
    readonly description?: string;
    readonly onGetStarted: () => void;
}

const STEPS = [
    { title: "Choose your Dataset", body: "Datasets should contain your expected prompts and desired responses." },
    { title: "Create the Experiment", body: "Compare performance across different models, evaluators, and more." },
    { title: "Run the Experiment", body: "Run experiments and see the results." },
] as const;

export function EvalOnboardingCard({
    heading = "Start evaluating agent performance with Experiments",
    description = "Tests help ensure agents respond consistently and reliably. Validate agent behaviour at creation and catch regressions after updates.",
    onGetStarted,
}: EvalOnboardingCardProps) {
    return (
        <div className="flex flex-col items-center gap-8 py-12">
            <div className="flex size-40 items-center justify-center rounded-full bg-(--secondary-w10)">
                <FlaskConical className="size-16 text-(--secondary-text-wMain)" />
            </div>
            <div className="flex max-w-xl flex-col items-center gap-2 text-center">
                <h3 className="text-xl font-semibold">{heading}</h3>
                <p className="text-sm text-(--secondary-text-wMain)">{description}</p>
            </div>
            <div className="w-full max-w-2xl rounded-xs border bg-(--secondary-w10)/40 p-6">
                <div className="mb-4 text-sm font-semibold">Create and Run Your First Experiment</div>
                <div className="flex flex-col gap-4">
                    {STEPS.map((step, idx) => (
                        <div key={step.title} className="flex gap-3">
                            <div className="flex size-6 shrink-0 items-center justify-center rounded-sm bg-(--brand-wMain) text-xs font-semibold text-white">{idx + 1}</div>
                            <div className="flex flex-col gap-0.5">
                                <div className="text-sm font-semibold">{step.title}</div>
                                <div className="text-sm text-(--secondary-text-wMain)">{step.body}</div>
                            </div>
                        </div>
                    ))}
                </div>
                <div className="mt-6 flex justify-end">
                    <Button onClick={onGetStarted}>
                        Get Started
                        <ArrowRight className="ml-1 size-4" />
                    </Button>
                </div>
            </div>
        </div>
    );
}
