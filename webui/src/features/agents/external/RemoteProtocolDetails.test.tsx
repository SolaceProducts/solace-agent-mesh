// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import type { RemoteAgentProtocolStatus } from "@/types";

import { CardRefusedBanner, FetchedCardDetails, OfferedInterfacesWarning, RemoteInterfaceInUse, RemoteProtocolSection } from "./RemoteProtocolDetails";

const status = (overrides: Partial<RemoteAgentProtocolStatus> = {}): RemoteAgentProtocolStatus => ({ selectedVersion: "v1_0", binding: "jsonrpc", deprecated: false, cardSignature: "unchecked", ...overrides });

const REFUSED_TEXT = "The agent card failed its signature check, so no tasks are sent to this agent. Check the agent's card signature and the Trusted JWKS URLs. The card is checked again automatically.";

describe("CardRefusedBanner", () => {
    test("warns when the deployed agent's card was refused", () => {
        render(<CardRefusedBanner status={status({ cardSignature: "refused" })} />);
        expect(screen.getByText(REFUSED_TEXT)).toBeInTheDocument();
    });

    test.each(["verified", "unsigned", "unchecked"] as const)("says nothing for a %s card or no report", cardSignature => {
        const { container } = render(<CardRefusedBanner status={status({ cardSignature })} />);
        expect(container).toBeEmptyDOMElement();
        const { container: none } = render(<CardRefusedBanner />);
        expect(none).toBeEmptyDOMElement();
    });
});

describe("RemoteInterfaceInUse", () => {
    test("shows the interface in use with an Older Version badge for A2A 0.3", () => {
        render(<RemoteInterfaceInUse status={status({ selectedVersion: "v0_3", deprecated: true })} />);
        expect(screen.getByText("Interface In Use")).toBeInTheDocument();
        expect(screen.getByTestId("sidePanelInterfaceInUse")).toHaveTextContent(/^A2A 0\.3 over JSON-RPCOlder Version$/);
    });

    test("shows A2A 1.0 with no badge, and nothing before the agent reported", () => {
        render(<RemoteInterfaceInUse status={status({ binding: "http_json" })} />);
        expect(screen.getByTestId("sidePanelInterfaceInUse")).toHaveTextContent(/^A2A 1\.0 over HTTP\+JSON$/);
        const { container } = render(<RemoteInterfaceInUse />);
        expect(container).toBeEmptyDOMElement();
    });
});

describe("RemoteProtocolSection", () => {
    test("shows the pin, what the card offers, what is in use and the signature outcome", () => {
        render(
            <RemoteProtocolSection
                hasAgentCard
                protocolVersion="v0_3"
                trustedCardJwksUrls={["https://keys.example.com/jwks.json"]}
                requireSignedCard
                offered={[
                    { version: "v1_0", binding: "http_json" },
                    { version: "v0_3", binding: "jsonrpc" },
                ]}
                status={status({ selectedVersion: "v0_3", deprecated: true, cardSignature: "verified" })}
                showStatus
            />
        );
        expect(screen.getByText("A2A Version").nextElementSibling).toHaveTextContent(/^A2A 0\.3$/);
        expect(screen.getByTestId("remoteProtocolOffered")).toHaveTextContent("A2A 1.0 over HTTP+JSON");
        expect(screen.getByTestId("remoteProtocolInUse")).toHaveTextContent("A2A 0.3 over JSON-RPC");
        expect(screen.getByTestId("remoteProtocolInUse")).toHaveTextContent("Older Version");
        expect(screen.getByTestId("remoteProtocolTrustedKeySets")).toHaveTextContent("https://keys.example.com/jwks.json");
        expect(screen.getByText("Yes")).toBeInTheDocument();
        expect(screen.getByTestId("remoteProtocolCardSignature")).toHaveTextContent("Verified");
    });

    test.each([
        ["verified", "Verified"],
        ["unsigned", "Unsigned"],
        ["unchecked", "Not checked"],
        ["refused", "Refused: tasks are not sent to the agent"],
    ] as const)("labels a %s card signature %s", (cardSignature, label) => {
        render(<RemoteProtocolSection hasAgentCard trustedCardJwksUrls={["https://keys.example.com/jwks.json"]} status={status({ cardSignature })} showStatus />);
        expect(screen.getByTestId("remoteProtocolCardSignature")).toHaveTextContent(new RegExp(`^${label}$`));
    });

    test.each([
        [true, "The agent mesh has not accepted a card yet. If the agent is not running, its card may have failed the signature check or be unsigned. Check the agent's logs for the reason."],
        [false, "The agent mesh has not accepted a card yet. If the agent is not running, its card may have failed the signature check. Check the agent's logs for the reason."],
    ])("says where to look when a card has not been accepted (require signed card: %s)", (requireSignedCard, sentence) => {
        render(<RemoteProtocolSection hasAgentCard trustedCardJwksUrls={["https://keys.example.com/jwks.json"]} requireSignedCard={requireSignedCard} showStatus />);
        expect(screen.getByTestId("remoteProtocolInUse")).toHaveTextContent(/^Not reported yet$/);
        expect(screen.getByTestId("remoteProtocolCardSignature")).toHaveTextContent(sentence, { normalizeWhitespace: true });
        expect(screen.getByTestId("remoteProtocolCardSignature").textContent).toBe(sentence);
    });

    test("without trusted key sets the signature is not checked", () => {
        render(<RemoteProtocolSection hasAgentCard trustedCardJwksUrls={[]} showStatus offered={[]} />);
        expect(screen.getByText("None: the card signature is not checked")).toBeInTheDocument();
        expect(screen.getByTestId("remoteProtocolCardSignature")).toHaveTextContent("Not checked");
        expect(screen.getByText("No A2A interface the agent mesh can use")).toBeInTheDocument();
        expect(screen.getByText("Automatic")).toBeInTheDocument();
    });

    test("a card-less agent shows no card trust and a not-yet-deployed one no report", () => {
        render(<RemoteProtocolSection hasAgentCard={false} protocolVersion="v1_0" trustedCardJwksUrls={[]} />);
        expect(screen.getByText("A2A 1.0")).toBeInTheDocument();
        expect(screen.queryByText("Trusted JWKS URLs")).toBeNull();
        expect(screen.queryByTestId("remoteProtocolInUse")).toBeNull();
    });
});

describe("FetchedCardDetails", () => {
    test("says nothing when the card preview carries no interface list", () => {
        const { container } = render(<FetchedCardDetails signed />);
        expect(container).toBeEmptyDOMElement();
    });

    test("lists the interfaces and says the card is signed", () => {
        render(
            <FetchedCardDetails
                signed
                interfaces={[
                    { version: "v1_0", binding: "jsonrpc" },
                    { version: "v0_3", binding: "jsonrpc" },
                ]}
            />
        );
        expect(screen.getByText("Supported interfaces: A2A 1.0 over JSON-RPC, A2A 0.3 over JSON-RPC")).toBeInTheDocument();
        expect(screen.getByText("Card signature: Signed")).toBeInTheDocument();
    });

    test("says an unsigned card is not signed, and lists no interfaces when there are none", () => {
        render(<FetchedCardDetails interfaces={[]} />);
        expect(screen.getByTestId("offeredInterfacesNotice")).toHaveTextContent("Card signature: Not signed");
        expect(screen.queryByText(/Supported interfaces/)).toBeNull();
    });
});

describe("OfferedInterfacesWarning", () => {
    test("says nothing when the card preview carries no interface list", () => {
        const { container } = render(<OfferedInterfacesWarning />);
        expect(container).toBeEmptyDOMElement();
    });

    test("warns when the card offers nothing the agent mesh can use", () => {
        render(<OfferedInterfacesWarning interfaces={[]} />);
        expect(screen.getByText("The agent card offers no A2A interface the agent mesh can use, so the agent will not start. Ask the agent's owner to add an A2A 1.0 or 0.3 interface.")).toBeInTheDocument();
    });

    test("warns when the card offers only A2A 0.3", () => {
        render(<OfferedInterfacesWarning interfaces={[{ version: "v0_3", binding: "jsonrpc" }]} />);
        expect(screen.getByText("This agent offers only A2A 0.3, an older version of the protocol. It works, but ask its owner for an A2A 1.0 interface.")).toBeInTheDocument();
    });

    test("renders nothing when A2A 1.0 is offered", () => {
        const { container } = render(
            <OfferedInterfacesWarning
                className="mt-4"
                interfaces={[
                    { version: "v1_0", binding: "jsonrpc" },
                    { version: "v0_3", binding: "jsonrpc" },
                ]}
            />
        );
        expect(container).toBeEmptyDOMElement();
    });
});
