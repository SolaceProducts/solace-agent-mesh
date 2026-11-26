// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { EntrypointSetupBanner } from "./EntrypointSetupBanner";
import { TEAMS_CONFIG_DOCS_URL } from "./utils/teams";

export function TeamsGatewaySetupBanner({ className }: Readonly<{ className?: string }>) {
    return <EntrypointSetupBanner product="Microsoft Teams" generatedLine="An entrypoint endpoint will be generated after creation." docsUrl={TEAMS_CONFIG_DOCS_URL} testId="teams-gateway-setup-banner" className={className} />;
}
