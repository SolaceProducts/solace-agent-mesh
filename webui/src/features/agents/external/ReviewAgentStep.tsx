// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { PageSection } from "@/common/layout";

import type { ExternalAgentCard } from "@/types";

import type { AdditionalInfoFormData } from "./AdditionalInfoStep";
import type { ConnectAgentFormData } from "./ConnectAgentForm";
import { RemoteAgentViewContent } from "./RemoteAgentViewContent";

interface ReviewAgentStepProps {
    formData: Partial<ConnectAgentFormData>;
    additionalInfoFormData: Partial<AdditionalInfoFormData>;
    fetchedAgentCard?: ExternalAgentCard | null;
}

export const ReviewAgentStep = ({ formData, additionalInfoFormData, fetchedAgentCard }: ReviewAgentStepProps) => {
    return (
        <div className="flex max-w-1/2 min-w-[700px] flex-col gap-8 pb-4">
            <PageSection>
                <h2 className="text-xl font-semibold">Review Your Agent</h2>
                <p className="text-(--secondary-text-wMain)">Review and confirm your agent configuration.</p>
            </PageSection>

            <RemoteAgentViewContent formData={formData} additionalInfoFormData={additionalInfoFormData} fetchedAgentCard={fetchedAgentCard} />
        </div>
    );
};
