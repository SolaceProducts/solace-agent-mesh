// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { BadgeCheck, Bot, ChevronRight, ClipboardList, Cpu, Database, FlaskConical } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

interface InputTile {
    readonly icon: typeof Bot;
    readonly title: string;
    readonly body: string;
    readonly tint: string;
    readonly fg: string;
}

const INPUT_TILES: InputTile[] = [
    { icon: Bot, title: "Agent", body: "The agent you want to evaluate.", tint: "bg-(--accent-n2-wMain)/10", fg: "text-(--accent-n2-wMain)" },
    { icon: Database, title: "Dataset", body: "Test prompts with the expected responses.", tint: "bg-(--accent-n5-wMain)/10", fg: "text-(--accent-n5-wMain)" },
    { icon: BadgeCheck, title: "Evaluator", body: "How each answer gets scored.", tint: "bg-(--accent-n0-wMain)/10", fg: "text-(--accent-n0-wMain)" },
    { icon: Cpu, title: "Model", body: "The LLM powering your agent.", tint: "bg-(--accent-n3-wMain)/10", fg: "text-(--accent-n3-wMain)" },
];

interface TileProps {
    readonly icon: typeof Bot;
    readonly title: string;
    readonly body: string;
    readonly tint: string;
    readonly fg: string;
    readonly className?: string;
}

function Tile({ icon: Icon, title, body, tint, fg, className }: TileProps) {
    return (
        <div className={cn("flex min-w-0 items-start gap-2.5", className)}>
            <div className={cn("flex size-8 shrink-0 items-center justify-center rounded-xs", tint)}>
                <Icon className={cn("size-4", fg)} />
            </div>
            <div className="flex min-w-0 flex-col gap-0.5">
                <div className="text-sm font-semibold">{title}</div>
                <div className="text-xs text-(--secondary-text-wMain)">{body}</div>
            </div>
        </div>
    );
}

interface EvalFlowExplainerProps {
    readonly className?: string;
    readonly variant?: "full" | "compact";
}

function IconTile({ icon: Icon, tint, fg }: { icon: typeof Bot; tint: string; fg: string }) {
    return (
        <div className={cn("flex size-9 items-center justify-center rounded-xs", tint)}>
            <Icon className={cn("size-4", fg)} />
        </div>
    );
}

function CompactExplainer({ className }: { className?: string }) {
    return (
        <div className={cn("flex items-center gap-3", className)}>
            <div className="grid grid-cols-2 gap-2 rounded-xs border border-dashed p-2">
                {INPUT_TILES.map(t => (
                    <IconTile key={t.title} icon={t.icon} tint={t.tint} fg={t.fg} />
                ))}
            </div>
            <ChevronRight className="size-3.5 shrink-0 text-(--secondary-text-wMain)" aria-hidden />
            <IconTile icon={FlaskConical} tint="bg-(--accent-n0-wMain)/10" fg="text-(--accent-n0-wMain)" />
            <ChevronRight className="size-3.5 shrink-0 text-(--secondary-text-wMain)" aria-hidden />
            <div className="flex size-12 items-center justify-center rounded-xs bg-(--brand-w100)/10">
                <ClipboardList className="size-6 text-(--brand-w100)" />
            </div>
        </div>
    );
}

export function EvalFlowExplainer({ className, variant = "full" }: EvalFlowExplainerProps) {
    if (variant === "compact") return <CompactExplainer className={className} />;
    return (
        <div className={cn("flex items-center gap-4", className)}>
            <div className="grid min-w-0 flex-1 grid-cols-1 gap-3 rounded-xs border border-dashed p-4 sm:grid-cols-2">
                {INPUT_TILES.map(t => (
                    <Tile key={t.title} icon={t.icon} title={t.title} body={t.body} tint={t.tint} fg={t.fg} />
                ))}
            </div>
            <ChevronRight className="size-4 shrink-0 text-(--secondary-text-wMain)" aria-hidden />
            <div className="min-w-0 flex-1 rounded-xs border p-4">
                <Tile icon={FlaskConical} title="Experiment" body="Re-usable configuration that can be run to generate a report." tint="bg-(--accent-n0-wMain)/10" fg="text-(--accent-n0-wMain)" />
            </div>
            <ChevronRight className="size-4 shrink-0 text-(--secondary-text-wMain)" aria-hidden />
            <div className="flex max-w-[200px] min-w-0 flex-1 flex-col items-center gap-2 text-center">
                <div className="flex size-16 items-center justify-center rounded-xs bg-(--brand-w100)/10">
                    <ClipboardList className="size-8 text-(--brand-w100)" />
                </div>
                <div className="text-sm font-semibold">Report</div>
                <div className="text-xs text-(--secondary-text-wMain)">The results of your experiment with the dataset run through the agent and scored by your evaluator.</div>
            </div>
        </div>
    );
}
