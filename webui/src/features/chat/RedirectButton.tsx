// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Sparkles } from "lucide-react";
import { v4 as uuidv4 } from "uuid";

import { useChatContext } from "@/providers/contexts";

import { stashPendingPrefill } from "@/utils/redirectPrefill";
import { SESSION_STORAGE_KEYS } from "@/utils/sessionStorageKeys";

import { Button } from "@/ui";

import { appendUrlParams, parseSafeRedirectPath } from "./redirectRoute";

export interface RedirectData {
    type: "redirect";
    route: string;
    label: string;
    context: string;
    url_params?: Record<string, string>;
    artifacts?: string[];
    source_session_id?: string;
}

interface RedirectButtonProps {
    data: RedirectData;
}

export const RedirectButton: React.FC<RedirectButtonProps> = ({ data }) => {
    const { displayError } = useChatContext();

    const handleClick = async () => {
        try {
            const route = (data.route || "").trim();
            const destPath = parseSafeRedirectPath(route);
            if (!destPath) {
                displayError({ title: "Invalid redirect", error: "The server provided an unsafe redirect URL.", errorSource: "client" });
                return;
            }

            const destSessionId = uuidv4();

            if (data.artifacts?.length && data.source_session_id) {
                sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_ARTIFACTS, JSON.stringify({ destSessionId, sourceSessionId: data.source_session_id, filenames: data.artifacts }));
            }

            const sanitizedContext = typeof data.context === "string" ? data.context.replace(/<[^>]*>/g, "").slice(0, 10000) : "";
            sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT, sanitizedContext);
            sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_SESSION_ID, destSessionId);
            if (destPath.startsWith("/builder") || destPath.startsWith("/chat")) {
                stashPendingPrefill(sanitizedContext, destPath.startsWith("/builder") ? destSessionId : null);
            }

            const finalRoute = appendUrlParams(route, data.url_params);
            globalThis.location.href = `${globalThis.location.pathname}#${finalRoute}`;
        } catch (err) {
            console.error("[RedirectButton] handleClick failed:", err);
        }
    };

    return (
        <div className="my-3 flex justify-start">
            <Button variant="default" size="lg" onClick={handleClick} className="w-fit gap-2">
                <Sparkles className="h-4 w-4" />
                {data.label || "Go to Builder"}
            </Button>
        </div>
    );
};
