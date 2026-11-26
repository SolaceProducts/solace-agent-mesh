// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

interface ChoiceScoresListProps {
    readonly scores: Record<string, number>;
}

export function ChoiceScoresList({ scores }: ChoiceScoresListProps) {
    const entries = Object.entries(scores);
    if (entries.length === 0) return null;
    return (
        <div className="flex flex-col gap-2">
            <div className="flex items-center gap-2 px-4 text-xs text-(--secondary-text-wMain)">
                <span className="flex-1">Criteria Assessment</span>
                <span className="w-24 text-right">Score</span>
            </div>
            <div className="flex flex-col gap-2">
                {entries.map(([choice, score]) => (
                    <div key={choice} className="flex items-center gap-2 rounded-sm bg-(--secondary-w10) px-4 py-2">
                        <span className="flex-1 text-sm font-medium">{choice}</span>
                        <span className="w-24 text-right font-mono text-sm">{Math.round(score * 100)}%</span>
                    </div>
                ))}
            </div>
        </div>
    );
}
