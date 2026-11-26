// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

export function useClipboardCopy(onCopyCallback?: () => void) {
    const [copied, setCopied] = useState(false);
    const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

    useEffect(() => {
        return () => {
            if (timerRef.current) {
                clearTimeout(timerRef.current);
            }
        };
    }, []);

    const handleCopy = useCallback(
        async (text: string) => {
            if (!navigator.clipboard) {
                return;
            }

            try {
                await navigator.clipboard.writeText(text);
                setCopied(true);
                onCopyCallback?.();

                if (timerRef.current) {
                    clearTimeout(timerRef.current);
                }

                timerRef.current = setTimeout(() => setCopied(false), 2000);
            } catch {}
        },
        [onCopyCallback]
    );

    return { handleCopy, copied };
}
