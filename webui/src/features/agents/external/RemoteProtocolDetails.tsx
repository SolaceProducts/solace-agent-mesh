// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { RemoteAgentProtocolPin } from "@/api/remote-agents";

import { MessageBanner } from "@/common/banners";
import { PageLabel, PageSection } from "@/common/layout";
import { Deprecated, WarningLabel } from "@/common/status";

import type { RemoteAgentInterface, RemoteAgentProtocolStatus } from "@/types";

import { interfaceLabel, PROTOCOL_VERSION_OPTIONS, REQUIRE_SIGNED_CARD_LABEL, TRUSTED_JWKS_URLS_LABEL } from "./cardTrust";

const CARD_SIGNATURE_LABELS: Record<RemoteAgentProtocolStatus["cardSignature"], string> = {
    verified: "Verified",
    unsigned: "Unsigned",
    unchecked: "Not checked",
    refused: "Refused: tasks are not sent to the agent",
};

const PENDING_SIGNATURE_CHECKED = "The agent mesh has not accepted a card yet. If the agent is not running, its card may have failed the signature check. Check the agent's logs for the reason.";
const PENDING_SIGNATURE_REQUIRED = "The agent mesh has not accepted a card yet. If the agent is not running, its card may have failed the signature check or be unsigned. Check the agent's logs for the reason.";

export const CARD_REFUSED_MESSAGE = "The agent card failed its signature check, so no tasks are sent to this agent. Check the agent's card signature and the Trusted JWKS URLs. The card is checked again automatically.";

export const CardRefusedBanner = ({ status, className }: { status?: RemoteAgentProtocolStatus; className?: string }) =>
    status?.cardSignature === "refused" ? <MessageBanner variant="warning" className={className} message={CARD_REFUSED_MESSAGE} /> : null;

export const RemoteInterfaceInUse = ({ status }: { status?: RemoteAgentProtocolStatus }) =>
    status ? (
        <div className="col-span-2">
            <div className="mb-2 text-sm font-medium text-(--secondary-text-wMain)">Interface In Use</div>
            <div className="flex items-center gap-2" data-testid="sidePanelInterfaceInUse">
                {interfaceLabel({ version: status.selectedVersion, binding: status.binding })}
                {status.deprecated && <Deprecated>Older Version</Deprecated>}
            </div>
        </div>
    ) : null;

const PIN_LABELS = new Map<RemoteAgentProtocolPin, string>(PROTOCOL_VERSION_OPTIONS.map(option => [option.value as RemoteAgentProtocolPin, option.label]));
const DEFAULT_PIN: RemoteAgentProtocolPin = "auto";
const pinLabel = (pin: RemoteAgentProtocolPin | undefined) => PIN_LABELS.get(pin ?? DEFAULT_PIN) ?? PIN_LABELS.get(DEFAULT_PIN);

export const FetchedCardDetails = ({ interfaces, signed }: { interfaces?: RemoteAgentInterface[]; signed?: boolean }) =>
    interfaces ? (
        <div className="mt-1 font-normal" data-testid="offeredInterfacesNotice">
            {interfaces.length > 0 && <div>Supported interfaces: {interfaces.map(interfaceLabel).join(", ")}</div>}
            <div>Card signature: {signed ? "Signed" : "Not signed"}</div>
        </div>
    ) : null;

export const OfferedInterfacesWarning = ({ interfaces, className }: { interfaces?: RemoteAgentInterface[]; className?: string }) => {
    const warning = offeredInterfacesWarning(interfaces);
    return warning ? (
        <div className={className}>
            <WarningLabel>{warning}</WarningLabel>
        </div>
    ) : null;
};

function offeredInterfacesWarning(interfaces?: RemoteAgentInterface[]): string | undefined {
    if (!interfaces) return undefined;
    if (interfaces.length === 0) return "The agent card offers no A2A interface the agent mesh can use, so the agent will not start. Ask the agent's owner to add an A2A 1.0 or 0.3 interface.";
    if (interfaces.every(item => item.version === "v0_3")) return "This agent offers only A2A 0.3, an older version of the protocol. It works, but ask its owner for an A2A 1.0 interface.";
    return undefined;
}

interface RemoteProtocolSectionProps {
    hasAgentCard: boolean;
    protocolVersion?: RemoteAgentProtocolPin;
    trustedCardJwksUrls: string[];
    requireSignedCard?: boolean;
    offered?: RemoteAgentInterface[];
    status?: RemoteAgentProtocolStatus;
    showStatus?: boolean;
}

export const RemoteProtocolSection = ({ hasAgentCard, protocolVersion, trustedCardJwksUrls, requireSignedCard, offered, status, showStatus }: RemoteProtocolSectionProps) => (
    <PageSection>
        <div className="text-lg font-semibold">A2A Protocol</div>
        <div className="grid grid-cols-[200px_1fr] gap-4">
            <PageLabel>A2A Version</PageLabel>
            <div>{pinLabel(protocolVersion)}</div>

            {offered && (
                <>
                    <PageLabel>Supported Interfaces</PageLabel>
                    <div>
                        {offered.length > 0 ? (
                            <ul data-testid="remoteProtocolOffered">
                                {offered.map(item => (
                                    <li key={`${item.version}-${item.binding}`}>{interfaceLabel(item)}</li>
                                ))}
                            </ul>
                        ) : (
                            <span className="text-(--secondary-text-wMain)">No A2A interface the agent mesh can use</span>
                        )}
                    </div>
                </>
            )}

            {showStatus && (
                <>
                    <PageLabel>Interface In Use</PageLabel>
                    <div data-testid="remoteProtocolInUse">
                        {status ? (
                            <span className="flex items-center gap-2">
                                {interfaceLabel({ version: status.selectedVersion, binding: status.binding })}
                                {status.deprecated && <Deprecated>Older Version</Deprecated>}
                            </span>
                        ) : (
                            <span className="text-(--secondary-text-wMain)">Not reported yet</span>
                        )}
                    </div>
                </>
            )}

            {hasAgentCard && (
                <>
                    <PageLabel>{TRUSTED_JWKS_URLS_LABEL}</PageLabel>
                    <div className="break-all">
                        {trustedCardJwksUrls.length > 0 ? (
                            <ul data-testid="remoteProtocolTrustedKeySets">
                                {trustedCardJwksUrls.map((url, index) => (
                                    <li key={`${index}-${url}`}>{url}</li>
                                ))}
                            </ul>
                        ) : (
                            <span className="text-(--secondary-text-wMain)">None: the card signature is not checked</span>
                        )}
                    </div>

                    <PageLabel>{REQUIRE_SIGNED_CARD_LABEL}</PageLabel>
                    <div>{requireSignedCard ? "Yes" : "No"}</div>

                    {showStatus && (
                        <>
                            <PageLabel>Card Signature</PageLabel>
                            <div data-testid="remoteProtocolCardSignature">
                                {status ? CARD_SIGNATURE_LABELS[status.cardSignature] : <CardSignaturePending requireSignedCard={!!requireSignedCard} hasTrustedKeySets={trustedCardJwksUrls.length > 0} />}
                            </div>
                        </>
                    )}
                </>
            )}
        </div>
    </PageSection>
);

const CardSignaturePending = ({ requireSignedCard, hasTrustedKeySets }: { requireSignedCard: boolean; hasTrustedKeySets: boolean }) => {
    if (!hasTrustedKeySets) return <span className="text-(--secondary-text-wMain)">Not checked</span>;
    return <span className="text-(--secondary-text-wMain)">{requireSignedCard ? PENDING_SIGNATURE_REQUIRED : PENDING_SIGNATURE_CHECKED}</span>;
};
