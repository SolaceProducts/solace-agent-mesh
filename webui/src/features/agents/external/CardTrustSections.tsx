// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Controller, useFormContext, useWatch } from "react-hook-form";

import type { RemoteAgentResponse } from "@/api/remote-agents";

import { CustomCheckbox } from "@/common/CustomCheckbox";
import { PageSection } from "@/common/layout";

import { Textarea } from "@/ui/textarea";

import type { AdditionalInfoFormData } from "./AdditionalInfoStep";
import { CARDLESS_PROTOCOL_VERSION_OPTIONS, MAX_TRUSTED_KEY_SET_URLS, PROTOCOL_VERSION_OPTIONS, REQUIRE_SIGNED_CARD_LABEL, TRUSTED_JWKS_URLS_LABEL } from "./cardTrust";
import { FormField, SelectField } from "./FormComponents";

const VERSION_ONLY_DESCRIPTION = "Select which version of the Agent-to-Agent (A2A) protocol the agent mesh uses with this agent. Without an agent card, Automatic uses A2A 0.3.";
const WITH_SIGNATURE_DESCRIPTION = "Select which version of the Agent-to-Agent (A2A) protocol the agent mesh uses with this agent, and whether to check the signature of its agent card.";
export const REQUIRE_SIGNED_CARD_HELP =
    "Check the signature of the agent card before sending tasks to the agent. A card that is unsigned, or not signed by a key from the trusted JWKS URLs, is refused, and no tasks are sent until a valid card is accepted.";
export const TRUSTED_JWKS_URLS_HELP = `Enter one JSON Web Key Set (JWKS) URL per line, up to ${MAX_TRUSTED_KEY_SET_URLS} URLs. Use https (http only on a loopback host such as localhost). The agent card must be signed by a key from one of these sets.`;
export const TRUSTED_JWKS_URLS_HINT = "For example, https://agent.example.com/.well-known/jwks.json";

type StoredCardTrust = Pick<RemoteAgentResponse, "trustedCardJwksUrls" | "requireSignedCard">;

function storedWithUrlsAndNoRequirement(storedAgent?: StoredCardTrust): boolean {
    return !storedAgent?.requireSignedCard && (storedAgent?.trustedCardJwksUrls?.length ?? 0) > 0;
}

export const A2AProtocolSection = ({ hasAgentCard, storedAgent }: { hasAgentCard: boolean; storedAgent?: StoredCardTrust }) => {
    const {
        control,
        register,
        trigger,
        setValue,
        formState: { errors },
    } = useFormContext<AdditionalInfoFormData>();
    const keepsStoredUrls = storedWithUrlsAndNoRequirement(storedAgent);
    const requireSignedCard = useWatch<AdditionalInfoFormData, "requireSignedCard">({ control, name: "requireSignedCard" });
    const showTrustedUrls = !!requireSignedCard || keepsStoredUrls;
    return (
        <PageSection>
            <div className="font-semibold">A2A Protocol</div>
            <div className="text-sm text-(--secondary-text-wMain)">{hasAgentCard ? WITH_SIGNATURE_DESCRIPTION : VERSION_ONLY_DESCRIPTION}</div>
            <div className="space-y-4">
                <SelectField name="protocolVersion" label="A2A Version" control={control} options={hasAgentCard ? PROTOCOL_VERSION_OPTIONS : CARDLESS_PROTOCOL_VERSION_OPTIONS} error={errors.protocolVersion} trigger={trigger} />
                {hasAgentCard && (
                    <>
                        <FormField label={REQUIRE_SIGNED_CARD_LABEL} error={errors.requireSignedCard} labelHelp={REQUIRE_SIGNED_CARD_HELP}>
                            <Controller
                                name="requireSignedCard"
                                control={control}
                                render={({ field }) => (
                                    <CustomCheckbox
                                        ariaLabel={REQUIRE_SIGNED_CARD_LABEL}
                                        checked={!!field.value}
                                        onChange={() => {
                                            const required = !field.value;
                                            field.onChange(required);
                                            if (required) return;
                                            if (!keepsStoredUrls) setValue("trustedCardJwksUrls", "");
                                            void trigger("trustedCardJwksUrls");
                                        }}
                                    />
                                )}
                            />
                        </FormField>
                        {showTrustedUrls && (
                            <FormField label={TRUSTED_JWKS_URLS_LABEL} required={!!requireSignedCard} error={errors.trustedCardJwksUrls} labelHelp={TRUSTED_JWKS_URLS_HELP} helpText={TRUSTED_JWKS_URLS_HINT}>
                                <Textarea {...register("trustedCardJwksUrls", { onBlur: () => trigger("trustedCardJwksUrls") })} rows={3} aria-invalid={!!errors.trustedCardJwksUrls} data-testid="externalAgentInput-trustedCardJwksUrls" />
                            </FormField>
                        )}
                    </>
                )}
            </div>
        </PageSection>
    );
};
