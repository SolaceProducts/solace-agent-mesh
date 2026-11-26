// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { CircleHelp, ClipboardList, ExternalLink } from "lucide-react";

import { useChatContext } from "@/providers/contexts";

import { reportAndNotifyError } from "@/common/product-analytics/reportAndNotifyError";

import { openExternalLink } from "@/utils/openExternalLink";

import { Button } from "@/ui";

const SOLACE_SUPPORT_URL = "https://solace.com/support/";

type DesktopSupportBindings = {
    OpenDiagnosticsFolder?: () => Promise<void>;
    SaveDiagnosticsZip?: () => Promise<string>;
};

const desktopApp = (): DesktopSupportBindings => (globalThis as { go?: { desktop?: { App?: DesktopSupportBindings } } }).go?.desktop?.App ?? {};

const errorMessage = (err: unknown) => (err instanceof Error ? err.message : String(err));

export const SupportSettings = () => {
    const { addNotification } = useChatContext();
    const [saving, setSaving] = useState(false);

    const openDiagnosticsFolder = async () => {
        try {
            await desktopApp().OpenDiagnosticsFolder?.();
        } catch (err) {
            reportAndNotifyError(addNotification, `Could not open the diagnostics folder: ${errorMessage(err)}`, "client");
        }
    };

    const saveDiagnosticsZip = async () => {
        setSaving(true);
        try {
            const path = await desktopApp().SaveDiagnosticsZip?.();
            if (path) {
                addNotification(`Diagnostics zip saved to ${path}`, "success");
            }
        } catch (err) {
            reportAndNotifyError(addNotification, `Could not save the diagnostics zip: ${errorMessage(err)}`, "client");
        } finally {
            setSaving(false);
        }
    };

    return (
        <div className="flex flex-col gap-8">
            <section className="flex flex-col gap-3">
                <h5 className="flex items-center gap-2 font-semibold">
                    <CircleHelp className="size-5" />
                    Customer Support
                </h5>
                <p>
                    Contact Solace support for help with technical issues, account questions, or product feedback.{" "}
                    <a
                        href={SOLACE_SUPPORT_URL}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="inline-flex items-center gap-1 font-semibold text-(--primary-wMain) hover:underline"
                        onClick={event => {
                            event.preventDefault();
                            openExternalLink(SOLACE_SUPPORT_URL);
                        }}
                    >
                        Contact Solace <ExternalLink className="size-4" />
                    </a>
                </p>
            </section>

            <section className="flex flex-col gap-3">
                <h5 className="flex items-center gap-2 font-semibold">
                    <ClipboardList className="size-5" />
                    Diagnostics
                </h5>
                <p>Application logs and crash reports are stored in a diagnostics folder on this computer. If something is not working as expected, download the diagnostics zip to investigate the issue or include it when requesting help.</p>
                <p className="text-sm text-(--secondary-text-wMain)">Conversations, files, API keys, and other credentials are never included.</p>
                <div className="mt-1 flex flex-wrap gap-3">
                    <Button variant="outline" onClick={() => void openDiagnosticsFolder()}>
                        Open Diagnostics Folder
                    </Button>
                    <Button onClick={() => void saveDiagnosticsZip()} loading={saving}>
                        Download Diagnostics Zip
                    </Button>
                </div>
            </section>
        </div>
    );
};
