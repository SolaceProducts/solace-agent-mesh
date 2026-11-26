// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

interface ProgressBarProps {
    readonly current: number;
    readonly total: number;
    readonly showLabel?: boolean;
}

export function ProgressBar({ current, total, showLabel = true }: ProgressBarProps) {
    const percent = total > 0 ? Math.min(Math.round((current / total) * 100), 100) : 0;
    return (
        <div className="flex items-center gap-2">
            <div className="h-1.5 min-w-[80px] flex-1 rounded-full bg-(--secondary-w10)">
                <div className="h-full rounded-full bg-(--brand-w100) transition-all" style={{ width: `${percent}%` }} />
            </div>
            {showLabel && (
                <span className="shrink-0 text-xs text-(--secondary-text-wMain)">
                    {current}/{total}
                </span>
            )}
        </div>
    );
}
