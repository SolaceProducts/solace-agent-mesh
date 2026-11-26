// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { EntrypointSetupBanner } from "./EntrypointSetupBanner";
import { WHATSAPP_CONFIG_DOCS_URL } from "./utils/whatsapp";

export function WhatsAppGatewaySetupBanner({ className }: Readonly<{ className?: string }>) {
    return <EntrypointSetupBanner product="WhatsApp" generatedLine="A webhook URL will be generated after creation." docsUrl={WHATSAPP_CONFIG_DOCS_URL} testId="whatsapp-gateway-setup-banner" className={className} />;
}
