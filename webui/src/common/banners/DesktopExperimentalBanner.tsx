// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCapabilities } from "@/api/capabilities";
import { useFeatureFlags } from "@/api/features";

import { useIsDesktop } from "@/hooks/useIsDesktop";
import { useLocalStorage } from "@/hooks/useLocalStorage";

import { EXTERNAL_DOC_LINKS } from "@/utils/docs";
import { newTabProps } from "@/utils/openExternalLink";

import { MessageBanner } from "./MessageBanner";

const HIDE_LINUX_STAGE_LABEL_FLAG = "hide_linux_desktop_stage_label";

export function DesktopExperimentalBanner() {
    const isDesktop = useIsDesktop();
    const { data } = useCapabilities();
    const { data: flags } = useFeatureFlags();
    const [dismissed, setDismissed] = useLocalStorage("desktop-experimental-banner-dismissed", false);
    const platform = data?.find(capability => capability.platform)?.platform;
    const labelShown = flags?.find(flag => flag.key === HIDE_LINUX_STAGE_LABEL_FLAG)?.resolved === false;

    if (!isDesktop || platform !== "linux" || !labelShown || dismissed) return null;

    return (
        <MessageBanner
            variant="info"
            style={{ alignItems: "center" }}
            message={
                <span>
                    Agent Mesh Desktop on Linux is{" "}
                    <a href={EXTERNAL_DOC_LINKS.releaseStages} {...newTabProps()} className="underline">
                        Experimental
                    </a>{" "}
                    and under active development. Its packaging and behavior are subject to change.
                </span>
            }
            dismissible
            onDismiss={() => setDismissed(true)}
        />
    );
}
