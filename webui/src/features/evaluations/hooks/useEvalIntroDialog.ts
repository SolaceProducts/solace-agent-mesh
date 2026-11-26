// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useState } from "react";

import { evaluationsApi } from "@/api/evaluations";

import { useLocalStorage } from "@/hooks/useLocalStorage";

export function useEvalIntroDialog() {
    const [evalIntroOpen, setEvalIntroOpen] = useState(false);
    const [evalIntroDismissed, setEvalIntroDismissed] = useLocalStorage("eval-intro-dialog-dismissed", false);

    const tryOpenEvalIntro = useCallback(
        async (agentName: string | undefined) => {
            if (!agentName) return;
            if (evalIntroDismissed) return;

            try {
                const experiments = await evaluationsApi.getExperiments({ targetAgent: agentName });
                if (experiments.length > 0) return;
            } catch (err) {
                console.warn("Failed to fetch experiments for agent", agentName, err);
            }

            setEvalIntroOpen(true);
        },
        [evalIntroDismissed]
    );

    return { evalIntroOpen, setEvalIntroOpen, tryOpenEvalIntro, evalIntroDismissed, setEvalIntroDismissed };
}
