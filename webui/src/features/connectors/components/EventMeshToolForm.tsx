// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useRef, useState } from "react";
import { AlertTriangle, Download } from "lucide-react";

import { PageLabel, PageLabelWithValue } from "@/common/layout";
import { ErrorLabel } from "@/common/status";

import { AccordionCard, Button, CodeEditor, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Textarea } from "@/ui";

import { EVENT_MESH_FIELD_HELP, EVENT_MESH_FIELD_HINT, EVENT_MESH_SECTION_TITLE } from "../eventMeshStyles";
import {
    EVENT_MESH_DELIVERY_MODE_OPTIONS,
    EVENT_MESH_PAYLOAD_FORMAT_OPTIONS,
    EVENT_MESH_REPLY_MODE_OPTIONS,
    EVENT_MESH_RESPONSE_MODE_OPTIONS,
    type EventMeshOperation,
    eventMeshOperationFieldError,
    eventMeshOperationSchemaError,
    parseEventMeshInputSchemaJSON,
    responseModeValue,
    type SelectOption,
} from "../eventMeshUtils";

interface EventMeshToolFormProps {
    operation: EventMeshOperation;
    onChange: (patch: Partial<EventMeshOperation>) => void;
    hasDuplicateName?: boolean;
    showValidation?: boolean;
}

const renderOptions = (options: SelectOption[]) =>
    options.map(option => (
        <SelectItem key={option.value} value={option.value}>
            {option.label}
        </SelectItem>
    ));

export function EventMeshToolForm({ operation, onChange, hasDuplicateName, showValidation }: Readonly<EventMeshToolFormProps>) {
    const [schemaDraft, setSchemaDraft] = useState(() => JSON.stringify(operation.input_schema, null, 2));
    const [schemaError, setSchemaError] = useState("");
    const [importError, setImportError] = useState("");
    const fileInputRef = useRef<HTMLInputElement | null>(null);

    const gate = (message: string) => (showValidation ? message : "");
    const toolNameError = gate(eventMeshOperationFieldError(operation, "tool_name") || (hasDuplicateName ? "Tool names must be unique" : ""));
    const toolDescriptionError = gate(eventMeshOperationFieldError(operation, "tool_description"));
    const topicError = gate(eventMeshOperationFieldError(operation, "topic"));
    const timeoutError = gate(eventMeshOperationFieldError(operation, "request_expiry_ms"));
    const schemaFieldError = schemaError || importError || gate(eventMeshOperationSchemaError(operation));

    const onSchemaDraftChange = (value: string) => {
        setSchemaDraft(value);
        try {
            onChange({ input_schema: parseEventMeshInputSchemaJSON(value), _input_schema_invalid: false });
            setSchemaError("");
        } catch (error) {
            setSchemaError(error instanceof Error ? error.message : "Invalid schema JSON");
            onChange({ _input_schema_invalid: true });
        }
    };

    const importSchemaFile = async (files: FileList | null) => {
        setImportError("");
        const file = files?.[0];
        if (!file) return;
        try {
            const inputSchema = parseEventMeshInputSchemaJSON(await file.text());
            onChange({ input_schema: inputSchema, _input_schema_invalid: false });
            setSchemaDraft(JSON.stringify(inputSchema, null, 2));
            setSchemaError("");
        } catch (error) {
            setImportError(error instanceof Error ? `Could not import schema: ${error.message}` : "Could not import schema file");
        }
    };

    return (
        <div className="w-[80%] min-w-0 space-y-8">
            <div>
                <div className={`${EVENT_MESH_SECTION_TITLE} mb-2`}>Tool Definition</div>
                <div className="space-y-6">
                    <PageLabelWithValue className="!gap-1">
                        <PageLabel required>Tool Name</PageLabel>
                        <Input value={operation.tool_name} onChange={event => onChange({ tool_name: event.target.value })} placeholder="get_order_status" aria-invalid={toolNameError ? true : undefined} />
                        <p className={EVENT_MESH_FIELD_HELP}>Name shown to the LLM. Format must be in lowercase: get_order_status</p>
                        {toolNameError && <ErrorLabel message={toolNameError} />}
                    </PageLabelWithValue>
                    <PageLabelWithValue className="!gap-1">
                        <PageLabel required>Tool Description</PageLabel>
                        <p className={EVENT_MESH_FIELD_HINT}>Description shown to the LLM explaining when to use this tool.</p>
                        <Textarea
                            value={operation.tool_description}
                            onChange={event => onChange({ tool_description: event.target.value })}
                            rows={3}
                            placeholder="Describe when the agent should use this tool."
                            aria-invalid={toolDescriptionError ? true : undefined}
                        />
                        {toolDescriptionError && <ErrorLabel message={toolDescriptionError} />}
                    </PageLabelWithValue>
                </div>
            </div>

            <div>
                <div className={`${EVENT_MESH_SECTION_TITLE} mb-2`}>Message Routing and Delivery</div>
                <div className="space-y-6">
                    <PageLabelWithValue className="!gap-1">
                        <PageLabel required>Topic Address</PageLabel>
                        <Input
                            value={operation.topic}
                            onChange={event => onChange({ topic: event.target.value })}
                            className="font-mono text-[13px]"
                            placeholder="acme/orders/v1/{order_id}/status"
                            spellCheck={false}
                            aria-invalid={topicError ? true : undefined}
                        />
                        <p className={EVENT_MESH_FIELD_HELP}>
                            Defines the topics that the connector can publish to. Use {"{variable}"} for variable substitution, e.g. acme/orders/v1/{"{order_id}"}/status.
                        </p>
                        {topicError && <ErrorLabel message={topicError} />}
                    </PageLabelWithValue>
                    <PageLabelWithValue className="!gap-1">
                        <PageLabel required>Response Mode</PageLabel>
                        <p className={EVENT_MESH_FIELD_HINT}>By default, guaranteed messaging is used for both request-reply and publish-subscribe response modes. You can change this in Advanced Settings.</p>
                        <Select value={responseModeValue(operation.wait_for_response)} onValueChange={value => onChange({ wait_for_response: value === "request_reply" })}>
                            <SelectTrigger>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>{renderOptions(EVENT_MESH_RESPONSE_MODE_OPTIONS)}</SelectContent>
                        </Select>
                    </PageLabelWithValue>
                </div>
            </div>

            <div>
                <div className={`${EVENT_MESH_SECTION_TITLE} mb-2`}>Input Schema</div>
                <input
                    ref={fileInputRef}
                    type="file"
                    accept=".json,application/json"
                    className="hidden"
                    onChange={event => {
                        void importSchemaFile(event.target.files);
                        event.target.value = "";
                    }}
                />
                <PageLabelWithValue className="!gap-1">
                    <div className="flex items-center justify-between">
                        <PageLabel required>Schema Configuration</PageLabel>
                        <Button type="button" size="sm" variant="ghost" onClick={() => fileInputRef.current?.click()}>
                            <Download className="mr-2 size-4" />
                            Import from File
                        </Button>
                    </div>
                    <p className={EVENT_MESH_FIELD_HINT}>Define the schemas this connector receives when called by the LLM. Schemas are defined using JSON.</p>
                    <CodeEditor
                        value={schemaDraft}
                        onChange={event => onSchemaDraftChange(event.target.value)}
                        rows={16}
                        aria-label="Tool input JSON Schema"
                        placeholder='{"type":"object","properties":{"action":{"type":"string","enum":["status","add"],"x-solace-payload-path":"request.action"}},"required":["action"]}'
                    />
                </PageLabelWithValue>
                {schemaFieldError && <ErrorLabel message={schemaFieldError} />}
            </div>

            <div className="overflow-hidden rounded-[4px] border bg-(--background-w10) p-4">
                <AccordionCard
                    value="advanced"
                    variant="borderless"
                    trigger={
                        <span className="flex items-center gap-2">
                            <span className={EVENT_MESH_SECTION_TITLE}>Advanced Settings</span>
                            {timeoutError && <AlertTriangle className="size-3.5 shrink-0 text-(--warning-wMain)" aria-label={timeoutError} />}
                        </span>
                    }
                >
                    <div className="flex flex-col gap-6 py-2">
                        {operation.wait_for_response && (
                            <PageLabelWithValue className="!gap-1">
                                <PageLabel required>Timeout</PageLabel>
                                <Input type="number" value={operation.request_expiry_ms ?? 60000} onChange={event => onChange({ request_expiry_ms: Number(event.target.value) })} aria-invalid={timeoutError ? true : undefined} />
                                <p className={EVENT_MESH_FIELD_HELP}>Request timeout in milliseconds.</p>
                                {timeoutError && <ErrorLabel message={timeoutError} />}
                            </PageLabelWithValue>
                        )}
                        {operation.wait_for_response && (
                            <PageLabelWithValue className="!gap-1">
                                <PageLabel required>Reply Delivery</PageLabel>
                                <Select value={operation.reply_mode ?? "temp_queue"} onValueChange={value => onChange({ reply_mode: value as EventMeshOperation["reply_mode"] })}>
                                    <SelectTrigger>
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>{renderOptions(EVENT_MESH_REPLY_MODE_OPTIONS)}</SelectContent>
                                </Select>
                                <p className={EVENT_MESH_FIELD_HELP}>How replies are received</p>
                            </PageLabelWithValue>
                        )}
                        <PageLabelWithValue className="!gap-1">
                            <PageLabel required>Payload Schema</PageLabel>
                            <p className={EVENT_MESH_FIELD_HINT}>The format of the payload sent at runtime.</p>
                            <Select value={operation.payload_format} onValueChange={value => onChange({ payload_format: value as EventMeshOperation["payload_format"] })}>
                                <SelectTrigger className="w-64">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>{renderOptions(EVENT_MESH_PAYLOAD_FORMAT_OPTIONS)}</SelectContent>
                            </Select>
                        </PageLabelWithValue>
                        <PageLabelWithValue className="!gap-1">
                            <PageLabel required>Delivery Mode</PageLabel>
                            <p className={EVENT_MESH_FIELD_HINT}>How the request is delivered to the broker</p>
                            <Select value={String(operation.qos ?? 1)} onValueChange={value => onChange({ qos: value === "0" ? 0 : 1 })}>
                                <SelectTrigger className="w-64">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>{renderOptions(EVENT_MESH_DELIVERY_MODE_OPTIONS)}</SelectContent>
                            </Select>
                        </PageLabelWithValue>
                    </div>
                </AccordionCard>
            </div>
        </div>
    );
}
