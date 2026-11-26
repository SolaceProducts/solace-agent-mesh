// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { DOC_LINKS } from "@/utils";

import { EntrypointSetupBanner } from "./EntrypointSetupBanner";

export function GoogleChatSetupBanner({ className }: Readonly<{ className?: string }>) {
    return <EntrypointSetupBanner product="Google Chat" generatedLine="A webhook URL will be generated after creation." docsUrl={DOC_LINKS.buildingEntrypointsGoogleChat} testId="google-chat-setup-banner" className={className} />;
}
