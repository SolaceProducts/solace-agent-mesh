// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect } from "react";

import { useChatContext } from "@/providers/contexts";

import { reportAndNotifyError } from "@/common/product-analytics/reportAndNotifyError";

type FileDialogErrorDetail = { op: "open" | "save"; message?: string };

export function useDesktopFileDialogErrors(): void {
    const { addNotification } = useChatContext();

    useEffect(() => {
        const handler = (e: Event) => {
            const detail = (e as CustomEvent<FileDialogErrorDetail>).detail;
            const action = detail?.op === "save" ? "save the file" : "open the file picker";
            const suffix = detail?.message ? `: ${detail.message}` : "";
            reportAndNotifyError(addNotification, `Could not ${action}${suffix}`, "client");
        };
        globalThis.addEventListener("desktop:fileDialogError", handler);
        return () => globalThis.removeEventListener("desktop:fileDialogError", handler);
    }, [addNotification]);
}
