// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useEffect, useMemo } from "react";
import { ExternalLink } from "lucide-react";
import { useWatch } from "react-hook-form";

import { useGateways } from "@/api/gateways";

import { useConfigContext } from "@/providers/contexts";

import { PAGE_COMMON_CLASSES, PageLabel, PageSection } from "@/common/layout";

import { MessageBanner } from "@/common";
import { AppForm, CheckboxField, ChipListField, NumberField, SelectField, type SelectOption, TextAreaField, TextField, useAppForm } from "@/forms";
import type { Gateway } from "@/types";
import { DOC_LINKS } from "@/utils";

import type { GatewayEditProps } from "../GatewayEdit";
import { type AgentOption, findAgentOption, isAgentNotDeployed, useAgentOptions } from "../hooks";

import { ConnectSection } from "./ConnectSection";
import { googleChatFormDefaults, hasStoredKey } from "./defaults";
import { parseDomainEntry } from "./domains";
import { addressSegment, endpointPrefix, isHttpsBase } from "./endpoint";
import { type GoogleChatDirty, type GoogleChatFormInput, type GoogleChatFormOutput, googleChatFormSchema, toGoogleChatValues } from "./schemas";

const NO_HTTPS_ADDRESS =
    "Google Chat can only send events to an https address, and this deployment's external web address (WEBUI_FRONTEND_SERVER_URL) is not set to one. Set it to the deployment's public https address, or create the entrypoint through YAML or the API with an endpoint URL.";

const URL_ENDPOINT_HELP = "Forms part of the address that Google Chat sends events to. It can't be changed after the entrypoint is created and it can't have a duplicated name.";

const NOT_DEPLOYED = "Not deployed";
const DESCRIPTION_MAX = 1000;

function agentItems(options: AgentOption[], value: string): SelectOption[] {
    const items = options.map(option => ({ value: option.name, label: isAgentNotDeployed(option) ? `${option.displayName} (${NOT_DEPLOYED})` : option.displayName }));
    if (!value || items.some(item => item.value === value)) return items;
    const known = findAgentOption(options, value);
    if (known) return items.map(item => (item.value === known.name ? { ...item, value } : item));
    return [...items, { value, label: `${value} (${NOT_DEPLOYED})` }];
}

function AgentField() {
    const options = useAgentOptions();
    const value = useWatch<GoogleChatFormInput, "values.default_agent_name">({ name: "values.default_agent_name" });
    const items = useMemo(() => agentItems(options, value ?? ""), [options, value]);
    return <SelectField name="values.default_agent_name" label="Assigned agent" description="The agent that handles messages when no specific agent is mentioned." required options={items} placeholder="Select an agent" />;
}

function Section({ title, children }: Readonly<{ title: string; children: ReactNode }>) {
    return (
        <PageSection className="mt-14">
            <div className="pb-2.5 text-lg font-semibold">{title}</div>
            <div className="space-y-6">{children}</div>
        </PageSection>
    );
}

function PrerequisitesNote({ text }: Readonly<{ text?: string }>) {
    return (
        <p className="text-sm">
            {text}{" "}
            <a href={DOC_LINKS.buildingEntrypointsGoogleChatPrerequisites} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 font-semibold text-(--primary-wMain) hover:underline">
                Learn about Google Chat prerequisites <ExternalLink className="size-4" />
            </a>
        </p>
    );
}

function SlugField({ isNew, prefix, segment }: Readonly<{ isNew: boolean; prefix: string; segment: string }>) {
    if (isNew) return <TextField name="slug" label="URL endpoint" description={URL_ENDPOINT_HELP} required prefix={prefix} />;
    return (
        <div>
            <PageLabel className="mb-1">URL endpoint</PageLabel>
            <div className="font-mono text-sm">{`${prefix}${segment}`}</div>
        </div>
    );
}

export function GoogleChatEdit({ schemaInfo, initialGatewayToEdit, isNew = false, onSubmit, onDirtyStateChange, footer }: Readonly<GatewayEditProps>) {
    const { googleChatEndpointBase } = useConfigContext();
    const storedKey = hasStoredKey(initialGatewayToEdit);
    const entrypointId = isNew ? undefined : initialGatewayToEdit?.id;
    const { data: gateways } = useGateways();
    const others = useMemo(() => (gateways ?? []).filter(gateway => gateway.id !== entrypointId), [gateways, entrypointId]);
    const takenSlugs = useMemo(() => new Set(others.flatMap(gateway => (gateway.slug ? [gateway.slug] : []))), [others]);
    const takenNames = useMemo(() => new Set(others.map(gateway => gateway.name)), [others]);
    const schema = useMemo(() => googleChatFormSchema({ isNew, hasStoredKey: storedKey, takenSlugs, takenNames }), [isNew, storedKey, takenSlugs, takenNames]);
    const initialValues = useMemo(() => googleChatFormDefaults(schemaInfo, initialGatewayToEdit, isNew), [schemaInfo, initialGatewayToEdit, isNew]);
    const prefix = endpointPrefix(googleChatEndpointBase || globalThis.location.origin);
    const form = useAppForm(schema, isNew ? { defaultValues: initialValues } : { values: initialValues });
    const { isDirty } = form.formState;

    useEffect(() => {
        onDirtyStateChange?.(isDirty);
    }, [isDirty, onDirtyStateChange]);

    const submit = (output: GoogleChatFormOutput) => {
        const dirty: GoogleChatDirty | null = isNew ? null : form.formState.dirtyFields;
        const gateway: Gateway = {
            ...(initialGatewayToEdit ?? { type: schemaInfo.type, values: {} }),
            name: output.name,
            description: output.description,
            slug: output.slug,
            values: isNew ? { ...toGoogleChatValues(output, dirty), endpoint_url: "" } : toGoogleChatValues(output, dirty),
        };
        return onSubmit(gateway);
    };

    return (
        <div className={PAGE_COMMON_CLASSES}>
            <AppForm form={form} onSubmit={submit} id="gateway-form" className="gap-0">
                <PageSection className="mt-8 space-y-6">
                    <div>
                        <PageLabel className="mb-1">Type</PageLabel>
                        <div data-testid="entrypointType">{schemaInfo.name}</div>
                    </div>
                    <TextField name="name" label="Name" required />
                    <TextAreaField name="description" label="Description" required maxLength={DESCRIPTION_MAX} />
                </PageSection>
                <Section title="Entrypoint Configuration">
                    {isNew && !isHttpsBase(googleChatEndpointBase) && <MessageBanner variant="warning" message={NO_HTTPS_ADDRESS} />}
                    <PrerequisitesNote text={schemaInfo.warning} />
                    <SlugField isNew={isNew} prefix={prefix} segment={addressSegment(initialGatewayToEdit?.slug, entrypointId)} />
                    <ConnectSection hasStoredKey={storedKey} />
                </Section>
                <Section title="Access">
                    <ChipListField name="values.allowed_email_domains" label="Allowed email domains" description="Only people at these domains can message the app. Leave empty to allow everyone." parseEntry={parseDomainEntry} />
                    <CheckboxField
                        name="values.use_user_identity_in_channels"
                        label="Use real user identity in spaces"
                        description="When not selected, messages in spaces run as an Agent Mesh service account. Direct messages always run as the sender."
                    />
                </Section>
                <Section title="Agent Settings">
                    <div className="md:w-1/2">
                        <AgentField />
                    </div>
                    <NumberField name="values.thread_history_limit" label="Thread history" description="The number of earlier thread messages that the agent reads. A value of 0 turns off thread history." min={0} max={100} />
                    <TextField
                        name="values.artifact_drive_folder_id"
                        label="Drive folder link"
                        description="Files that the agent creates go here and are shared with the person who asked for them. Use a shared drive only for these files, because anyone who can open the drive can open every file in it."
                    />
                </Section>
                {footer}
            </AppForm>
        </div>
    );
}
