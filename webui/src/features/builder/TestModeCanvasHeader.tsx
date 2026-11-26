// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Loader2 } from "lucide-react";

import { Button } from "@/ui/button";

interface Props {
    testTargetName: string;
    testTargetKind?: "workflow" | "agent";
    running: boolean;
    onStop: () => void;
}

export function TestModeCanvasHeader({ testTargetName, running, onStop }: Props) {
    return (
        <div className="pointer-events-none absolute top-3 right-0 left-0 z-20 flex items-center justify-center px-4">
            <div className="pointer-events-auto flex items-center gap-3 rounded-md border border-(--accent-n0-w30) bg-(--accent-n0-w10) px-4 py-1.5 text-sm shadow-sm">
                {running ? <Loader2 className="h-4 w-4 shrink-0 animate-spin text-(--success-wMain)" /> : <div className="h-4 w-4 shrink-0" aria-hidden />}
                <span className="text-(--primary-text-wMain)">
                    Testing <span className="font-bold">{testTargetName}</span>
                </span>
                <Button size="sm" variant="ghost" disabled={!running} onClick={onStop}>
                    Stop Test
                </Button>
            </div>
        </div>
    );
}
