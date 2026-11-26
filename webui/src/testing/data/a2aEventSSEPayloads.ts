// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { A2AEventSSEPayload, TaskFE } from "@/types";

export const BASE_TIME = "2024-06-15T10:00:00.000Z";

export function offsetTime(ms: number): string {
    return new Date(new Date(BASE_TIME).getTime() + ms).toISOString();
}

export function createEvent(overrides: Partial<A2AEventSSEPayload> = {}): A2AEventSSEPayload {
    return {
        event_type: "a2a_message",
        timestamp: BASE_TIME,
        solace_topic: "test/topic",
        direction: "response",
        source_entity: "Orchestrator",
        target_entity: "User",
        task_id: "task-1",
        payload_summary: {},
        full_payload: {},
        ...overrides,
    };
}

export function createTask(overrides: Partial<TaskFE> = {}): TaskFE {
    return {
        taskId: "task-1",
        initialRequestText: "Hello",
        events: [],
        firstSeen: new Date(BASE_TIME),
        lastUpdated: new Date(BASE_TIME),
        ...overrides,
    };
}

export function createRequestEvent(text: string, timestamp = BASE_TIME, taskId = "task-1", targetEntity = "Orchestrator"): A2AEventSSEPayload {
    return createEvent({
        direction: "request",
        timestamp,
        task_id: taskId,
        source_entity: "User",
        target_entity: targetEntity,
        payload_summary: { method: "message/send" },
        full_payload: {
            method: "message/send",
            params: {
                message: {
                    parts: [{ kind: "text", text }],
                },
            },
        },
    });
}

export function createSignalEvent(signalType: string, signalData: Record<string, unknown>, agent: string, timestamp: string, taskId = "task-1"): A2AEventSSEPayload {
    return createEvent({
        direction: "status-update",
        timestamp,
        task_id: taskId,
        source_entity: agent,
        full_payload: {
            result: {
                status: {
                    state: "working",
                    message: {
                        parts: [{ kind: "data", data: { type: signalType, ...signalData } }],
                        metadata: { agent_name: agent },
                    },
                },
                metadata: { agent_name: agent },
            },
        },
    });
}

export function createCompletedResponseEvent(text: string, agent = "Orchestrator", timestamp = BASE_TIME, taskId = "task-1"): A2AEventSSEPayload {
    return createEvent({
        direction: "response",
        timestamp,
        task_id: taskId,
        source_entity: agent,
        target_entity: "User",
        full_payload: {
            result: {
                status: {
                    state: "completed",
                    message: { parts: [{ kind: "text", text }] },
                },
                metadata: { agent_name: agent },
            },
        },
    });
}

export function createFailedResponseEvent(errorMessage: string, agent = "Orchestrator", timestamp = BASE_TIME, taskId = "task-1"): A2AEventSSEPayload {
    return createEvent({
        direction: "response",
        timestamp,
        task_id: taskId,
        source_entity: agent,
        target_entity: "User",
        full_payload: {
            result: {
                status: {
                    state: "failed",
                    message: { parts: [{ kind: "text", text: errorMessage }] },
                },
                metadata: { agent_name: agent },
            },
        },
    });
}

export function createStatusUpdateEvent(text: string, agent = "Orchestrator", timestamp = BASE_TIME, taskId = "task-1"): A2AEventSSEPayload {
    return createEvent({
        direction: "status-update",
        timestamp,
        task_id: taskId,
        source_entity: agent,
        full_payload: {
            result: {
                status: {
                    state: "working",
                    message: {
                        parts: [{ kind: "text", text }],
                        metadata: { agent_name: agent },
                    },
                },
                metadata: { agent_name: agent },
            },
        },
    });
}

const simpleToolCallEvents: A2AEventSSEPayload[] = [
    createRequestEvent("What is the weather in San Francisco?", offsetTime(0)),
    createSignalEvent(
        "llm_invocation",
        {
            request: {
                model: "claude-sonnet-4-6",
                contents: [{ role: "user", parts: [{ text: "What is the weather in San Francisco?" }] }],
            },
        },
        "Orchestrator",
        offsetTime(200)
    ),
    createSignalEvent(
        "llm_response",
        {
            data: {
                content: {
                    parts: [
                        {
                            function_call: {
                                id: "fc-weather-1",
                                name: "get_weather",
                                args: { city: "San Francisco" },
                            },
                        },
                    ],
                },
            },
        },
        "Orchestrator",
        offsetTime(1200)
    ),
    createSignalEvent(
        "tool_invocation_start",
        {
            function_call_id: "fc-weather-1",
            tool_name: "get_weather",
            tool_args: { city: "San Francisco" },
        },
        "Orchestrator",
        offsetTime(1300)
    ),
    createSignalEvent(
        "tool_result",
        {
            function_call_id: "fc-weather-1",
            tool_name: "get_weather",
            result_data: { temperature: "68°F", condition: "Partly cloudy", humidity: "65%" },
        },
        "Orchestrator",
        offsetTime(2000)
    ),
    createStatusUpdateEvent("The weather in San Francisco is 68°F and partly cloudy with 65% humidity.", "Orchestrator", offsetTime(2500)),
    createCompletedResponseEvent("The weather in San Francisco is 68°F and partly cloudy with 65% humidity.", "Orchestrator", offsetTime(3000)),
];

export const simpleToolCallTask = createTask({
    taskId: "task-1",
    initialRequestText: "What is the weather in San Francisco?",
    events: simpleToolCallEvents,
    lastUpdated: new Date(offsetTime(3000)),
});

export const simpleToolCallMonitoredTasks: Record<string, TaskFE> = {
    "task-1": simpleToolCallTask,
};

const peerDelegationParentEvents: A2AEventSSEPayload[] = [
    createRequestEvent("Validate order #5678 and check inventory", offsetTime(0)),
    createSignalEvent(
        "llm_invocation",
        {
            request: {
                model: "claude-sonnet-4-6",
                contents: [{ role: "user", parts: [{ text: "Validate order #5678 and check inventory" }] }],
            },
        },
        "Orchestrator",
        offsetTime(200)
    ),
    createSignalEvent(
        "llm_response",
        {
            data: {
                content: {
                    parts: [
                        {
                            function_call: {
                                id: "fc-validate-1",
                                name: "peer_ValidatorAgent",
                                args: { order_id: "5678" },
                            },
                        },
                    ],
                },
            },
        },
        "Orchestrator",
        offsetTime(1000)
    ),
    createSignalEvent(
        "tool_invocation_start",
        {
            function_call_id: "fc-validate-1",
            tool_name: "peer_ValidatorAgent",
            tool_args: { order_id: "5678" },
        },
        "Orchestrator",
        offsetTime(1100)
    ),
    createSignalEvent(
        "tool_result",
        {
            function_call_id: "fc-validate-1",
            tool_name: "peer_ValidatorAgent",
            result_data: { is_valid: true, items: ["Widget A", "Widget B"] },
        },
        "Orchestrator",
        offsetTime(3500)
    ),
    createStatusUpdateEvent("Order #5678 is valid with 2 items: Widget A and Widget B.", "Orchestrator", offsetTime(4000)),
    createCompletedResponseEvent("Order #5678 has been validated. It contains Widget A and Widget B, both in stock.", "Orchestrator", offsetTime(4500)),
];

const peerDelegationChildEvents: A2AEventSSEPayload[] = [
    createEvent({
        direction: "request",
        timestamp: offsetTime(1200),
        task_id: "task-2",
        source_entity: "Orchestrator",
        target_entity: "ValidatorAgent",
        payload_summary: { method: "message/send" },
        full_payload: {
            method: "message/send",
            params: {
                message: {
                    parts: [{ kind: "text", text: "Validate order #5678" }],
                    metadata: {
                        function_call_id: "fc-validate-1",
                        parentTaskId: "task-1",
                        agent_name: "ValidatorAgent",
                    },
                },
                metadata: {
                    function_call_id: "fc-validate-1",
                    parentTaskId: "task-1",
                },
            },
        },
    }),
    createSignalEvent(
        "llm_invocation",
        {
            request: {
                model: "claude-sonnet-4-6",
                contents: [{ role: "user", parts: [{ text: "Validate order #5678" }] }],
            },
        },
        "ValidatorAgent",
        offsetTime(1500),
        "task-2"
    ),
    createSignalEvent(
        "llm_response",
        {
            data: {
                content: {
                    parts: [{ text: "Order #5678 is valid. It contains Widget A and Widget B." }],
                },
            },
        },
        "ValidatorAgent",
        offsetTime(2500),
        "task-2"
    ),
    createCompletedResponseEvent("Order #5678 is valid.", "ValidatorAgent", offsetTime(3000), "task-2"),
];

const peerDelegationParentTask = createTask({
    taskId: "task-1",
    initialRequestText: "Validate order #5678 and check inventory",
    events: peerDelegationParentEvents,
    lastUpdated: new Date(offsetTime(4500)),
});

const peerDelegationChildTask = createTask({
    taskId: "task-2",
    initialRequestText: "Validate order #5678",
    events: peerDelegationChildEvents,
    firstSeen: new Date(offsetTime(1200)),
    lastUpdated: new Date(offsetTime(3000)),
    parentTaskId: "task-1",
});

export const peerDelegationMonitoredTasks: Record<string, TaskFE> = {
    "task-1": peerDelegationParentTask,
    "task-2": peerDelegationChildTask,
};

const taskFailedEvents: A2AEventSSEPayload[] = [
    createRequestEvent("Connect to the production database and fetch all user records", offsetTime(0)),
    createSignalEvent(
        "llm_invocation",
        {
            request: {
                model: "claude-sonnet-4-6",
                contents: [{ role: "user", parts: [{ text: "Connect to the production database and fetch all user records" }] }],
            },
        },
        "Orchestrator",
        offsetTime(200)
    ),
    createSignalEvent(
        "llm_response",
        {
            data: {
                content: {
                    parts: [
                        {
                            function_call: {
                                id: "fc-db-1",
                                name: "query_database",
                                args: { query: "SELECT * FROM users" },
                            },
                        },
                    ],
                },
            },
        },
        "Orchestrator",
        offsetTime(1000)
    ),
    createSignalEvent(
        "tool_invocation_start",
        {
            function_call_id: "fc-db-1",
            tool_name: "query_database",
            tool_args: { query: "SELECT * FROM users" },
        },
        "Orchestrator",
        offsetTime(1100)
    ),
    createFailedResponseEvent("Connection timeout: Unable to reach production database at db.example.internal:5432. Please verify network connectivity and database availability.", "Orchestrator", offsetTime(6000)),
];

export const taskFailedTask = createTask({
    taskId: "task-1",
    initialRequestText: "Connect to the production database and fetch all user records",
    events: taskFailedEvents,
    lastUpdated: new Date(offsetTime(6000)),
});

export const taskFailedMonitoredTasks: Record<string, TaskFE> = {
    "task-1": taskFailedTask,
};

const workflowEvents: A2AEventSSEPayload[] = [
    createRequestEvent("Run the data processing pipeline on Q4 sales data", offsetTime(0)),
    createSignalEvent(
        "workflow_execution_start",
        {
            workflow_name: "Data Processing Pipeline",
            execution_id: "exec-001",
            workflow_input: { dataset: "Q4 sales", format: "csv" },
        },
        "Orchestrator",
        offsetTime(500)
    ),
    createSignalEvent(
        "workflow_node_execution_start",
        {
            node_id: "validate-input",
            node_type: "agent",
            condition: "input.format in ['csv', 'json']",
        },
        "Orchestrator",
        offsetTime(1000)
    ),
    createSignalEvent(
        "workflow_node_execution_result",
        {
            node_id: "validate-input",
            status: "success",
            metadata: { condition: "input.format in ['csv', 'json']", condition_result: true },
        },
        "Orchestrator",
        offsetTime(2000)
    ),
    createSignalEvent(
        "workflow_node_execution_start",
        {
            node_id: "transform-data",
            node_type: "agent",
        },
        "Orchestrator",
        offsetTime(2500)
    ),
    createSignalEvent(
        "workflow_node_execution_result",
        {
            node_id: "transform-data",
            status: "success",
            metadata: { rows_processed: 15000, output_format: "parquet" },
        },
        "Orchestrator",
        offsetTime(5000)
    ),
    createSignalEvent(
        "workflow_execution_result",
        {
            status: "success",
            workflow_output: { rows_processed: 15000, output_path: "/data/output/q4_sales.parquet" },
        },
        "Orchestrator",
        offsetTime(5500)
    ),
    createCompletedResponseEvent("Pipeline completed successfully. Processed 15,000 rows of Q4 sales data.", "Orchestrator", offsetTime(6000)),
];

export const workflowTask = createTask({
    taskId: "task-1",
    initialRequestText: "Run the data processing pipeline on Q4 sales data",
    events: workflowEvents,
    lastUpdated: new Date(offsetTime(6000)),
});

export const workflowMonitoredTasks: Record<string, TaskFE> = {
    "task-1": workflowTask,
};

const inProgressEvents: A2AEventSSEPayload[] = [
    createRequestEvent("Analyze the customer feedback data and generate a sentiment report", offsetTime(0)),
    createSignalEvent(
        "llm_invocation",
        {
            request: {
                model: "claude-sonnet-4-6",
                contents: [{ role: "user", parts: [{ text: "Analyze the customer feedback data and generate a sentiment report" }] }],
            },
        },
        "Orchestrator",
        offsetTime(200)
    ),
    createSignalEvent(
        "llm_response",
        {
            data: {
                content: {
                    parts: [
                        {
                            function_call: {
                                id: "fc-analyze-1",
                                name: "analyze_sentiment",
                                args: { dataset: "customer_feedback" },
                            },
                        },
                    ],
                },
            },
        },
        "Orchestrator",
        offsetTime(1000)
    ),
    createSignalEvent(
        "tool_invocation_start",
        {
            function_call_id: "fc-analyze-1",
            tool_name: "analyze_sentiment",
            tool_args: { dataset: "customer_feedback" },
        },
        "Orchestrator",
        offsetTime(1100)
    ),
];

export const inProgressTask = createTask({
    taskId: "task-1",
    initialRequestText: "Analyze the customer feedback data and generate a sentiment report",
    events: inProgressEvents,
    lastUpdated: new Date(offsetTime(1100)),
});

export const inProgressMonitoredTasks: Record<string, TaskFE> = {
    "task-1": inProgressTask,
};

const artifactCreationEvents: A2AEventSSEPayload[] = [
    createRequestEvent("Generate a sales report PDF for Q4", offsetTime(0)),
    createSignalEvent(
        "llm_invocation",
        {
            request: {
                model: "claude-sonnet-4-6",
                contents: [{ role: "user", parts: [{ text: "Generate a sales report PDF for Q4" }] }],
            },
        },
        "Orchestrator",
        offsetTime(200)
    ),
    createSignalEvent(
        "llm_response",
        {
            data: {
                content: {
                    parts: [
                        {
                            function_call: {
                                id: "fc-report-1",
                                name: "generate_report",
                                args: { quarter: "Q4", format: "pdf" },
                            },
                        },
                    ],
                },
            },
        },
        "Orchestrator",
        offsetTime(1000)
    ),
    createSignalEvent(
        "tool_invocation_start",
        {
            function_call_id: "fc-report-1",
            tool_name: "generate_report",
            tool_args: { quarter: "Q4", format: "pdf" },
        },
        "Orchestrator",
        offsetTime(1100)
    ),
    createSignalEvent(
        "tool_result",
        {
            function_call_id: "fc-report-1",
            tool_name: "generate_report",
            result_data: { status: "success", filename: "q4_sales_report.pdf" },
        },
        "Orchestrator",
        offsetTime(3000)
    ),
    createSignalEvent(
        "artifact_saved",
        {
            filename: "q4_sales_report.pdf",
            version: 1,
            description: "Q4 Sales Report with charts and regional breakdown",
            mime_type: "application/pdf",
            function_call_id: "fc-report-1",
        },
        "Orchestrator",
        offsetTime(3200)
    ),
    createStatusUpdateEvent("Here is your Q4 sales report.", "Orchestrator", offsetTime(3500)),
    createCompletedResponseEvent("Here is your Q4 sales report. I've generated a PDF with charts and regional breakdown.", "Orchestrator", offsetTime(4000)),
];

export const artifactCreationTask = createTask({
    taskId: "task-1",
    initialRequestText: "Generate a sales report PDF for Q4",
    events: artifactCreationEvents,
    lastUpdated: new Date(offsetTime(4000)),
});

export const artifactCreationMonitoredTasks: Record<string, TaskFE> = {
    "task-1": artifactCreationTask,
};

const parallelToolCallEvents: A2AEventSSEPayload[] = [
    createRequestEvent("Get the weather in New York and London simultaneously", offsetTime(0)),
    createSignalEvent(
        "llm_invocation",
        {
            request: {
                model: "claude-sonnet-4-6",
                contents: [{ role: "user", parts: [{ text: "Get the weather in New York and London simultaneously" }] }],
            },
        },
        "Orchestrator",
        offsetTime(200)
    ),
    createSignalEvent(
        "llm_response",
        {
            data: {
                content: {
                    parts: [
                        {
                            function_call: {
                                id: "fc-ny-1",
                                name: "get_weather",
                                args: { city: "New York" },
                            },
                        },
                        {
                            function_call: {
                                id: "fc-london-1",
                                name: "get_weather",
                                args: { city: "London" },
                            },
                        },
                    ],
                },
            },
        },
        "Orchestrator",
        offsetTime(1000)
    ),
    createSignalEvent(
        "tool_invocation_start",
        {
            function_call_id: "fc-ny-1",
            tool_name: "get_weather",
            tool_args: { city: "New York" },
            parallel_group_id: "pg-weather-1",
        },
        "Orchestrator",
        offsetTime(1100)
    ),
    createSignalEvent(
        "tool_invocation_start",
        {
            function_call_id: "fc-london-1",
            tool_name: "get_weather",
            tool_args: { city: "London" },
            parallel_group_id: "pg-weather-1",
        },
        "Orchestrator",
        offsetTime(1100)
    ),
    createSignalEvent(
        "tool_result",
        {
            function_call_id: "fc-ny-1",
            tool_name: "get_weather",
            result_data: { temperature: "45°F", condition: "Rainy" },
        },
        "Orchestrator",
        offsetTime(1800)
    ),
    createSignalEvent(
        "tool_result",
        {
            function_call_id: "fc-london-1",
            tool_name: "get_weather",
            result_data: { temperature: "50°F", condition: "Overcast" },
        },
        "Orchestrator",
        offsetTime(2000)
    ),
    createSignalEvent(
        "llm_invocation",
        {
            request: {
                model: "claude-sonnet-4-6",
                contents: [
                    { role: "user", parts: [{ text: "Get the weather in New York and London simultaneously" }] },
                    { role: "model", parts: [{ function_call: { id: "fc-ny-1", name: "get_weather" } }, { function_call: { id: "fc-london-1", name: "get_weather" } }] },
                    { role: "tool", parts: [{ function_response: { name: "get_weather", response: { temperature: "45°F" } } }] },
                    { role: "tool", parts: [{ function_response: { name: "get_weather", response: { temperature: "50°F" } } }] },
                ],
            },
        },
        "Orchestrator",
        offsetTime(2200)
    ),
    createSignalEvent(
        "llm_response",
        {
            data: {
                content: {
                    parts: [{ text: "New York is 45°F and rainy. London is 50°F and overcast." }],
                },
                partial: false,
            },
        },
        "Orchestrator",
        offsetTime(2800)
    ),
    createStatusUpdateEvent("New York is 45°F and rainy. London is 50°F and overcast.", "Orchestrator", offsetTime(3000)),
    createCompletedResponseEvent("New York is 45°F and rainy. London is 50°F and overcast.", "Orchestrator", offsetTime(3500)),
];

export const parallelToolCallTask = createTask({
    taskId: "task-1",
    initialRequestText: "Get the weather in New York and London simultaneously",
    events: parallelToolCallEvents,
    lastUpdated: new Date(offsetTime(3500)),
});

export const parallelToolCallMonitoredTasks: Record<string, TaskFE> = {
    "task-1": parallelToolCallTask,
};

const parallelPeerParentEvents: A2AEventSSEPayload[] = [
    createRequestEvent("Check order #9999 validity and fetch shipping estimate", offsetTime(0)),
    createSignalEvent(
        "llm_invocation",
        {
            request: {
                model: "claude-sonnet-4-6",
                contents: [{ role: "user", parts: [{ text: "Check order #9999 validity and fetch shipping estimate" }] }],
            },
        },
        "Orchestrator",
        offsetTime(200)
    ),
    createSignalEvent(
        "llm_response",
        {
            data: {
                content: {
                    parts: [
                        {
                            function_call: {
                                id: "fc-validate-9",
                                name: "peer_ValidatorAgent",
                                args: { order_id: "9999" },
                            },
                        },
                        {
                            function_call: {
                                id: "fc-shipping-9",
                                name: "peer_ShippingAgent",
                                args: { order_id: "9999" },
                            },
                        },
                    ],
                },
            },
        },
        "Orchestrator",
        offsetTime(1000)
    ),
    createSignalEvent(
        "tool_invocation_start",
        {
            function_call_id: "fc-validate-9",
            tool_name: "peer_ValidatorAgent",
            tool_args: { order_id: "9999" },
        },
        "Orchestrator",
        offsetTime(1100)
    ),
    createSignalEvent(
        "tool_invocation_start",
        {
            function_call_id: "fc-shipping-9",
            tool_name: "peer_ShippingAgent",
            tool_args: { order_id: "9999" },
        },
        "Orchestrator",
        offsetTime(1100)
    ),
    createSignalEvent(
        "tool_result",
        {
            function_call_id: "fc-validate-9",
            tool_name: "peer_ValidatorAgent",
            result_data: { is_valid: true },
        },
        "Orchestrator",
        offsetTime(3000)
    ),
    createSignalEvent(
        "tool_result",
        {
            function_call_id: "fc-shipping-9",
            tool_name: "peer_ShippingAgent",
            result_data: { estimated_days: 3, carrier: "FedEx" },
        },
        "Orchestrator",
        offsetTime(3200)
    ),
    createStatusUpdateEvent("Order #9999 is valid. Estimated delivery: 3 days via FedEx.", "Orchestrator", offsetTime(3800)),
    createCompletedResponseEvent("Order #9999 is valid. Estimated delivery: 3 days via FedEx.", "Orchestrator", offsetTime(4000)),
];

const parallelPeerValidatorEvents: A2AEventSSEPayload[] = [
    createEvent({
        direction: "request",
        timestamp: offsetTime(1200),
        task_id: "task-pp-2",
        source_entity: "Orchestrator",
        target_entity: "ValidatorAgent",
        payload_summary: { method: "message/send" },
        full_payload: {
            method: "message/send",
            params: {
                message: {
                    parts: [{ kind: "text", text: "Validate order #9999" }],
                    metadata: { function_call_id: "fc-validate-9", parentTaskId: "task-1", agent_name: "ValidatorAgent" },
                },
                metadata: { function_call_id: "fc-validate-9", parentTaskId: "task-1" },
            },
        },
    }),
    createSignalEvent("llm_invocation", { request: { model: "claude-sonnet-4-6", contents: [{ role: "user", parts: [{ text: "Validate order #9999" }] }] } }, "ValidatorAgent", offsetTime(1500), "task-pp-2"),
    createSignalEvent("llm_response", { data: { content: { parts: [{ text: "Order #9999 is valid." }] } } }, "ValidatorAgent", offsetTime(2200), "task-pp-2"),
    createCompletedResponseEvent("Order #9999 is valid.", "ValidatorAgent", offsetTime(2500), "task-pp-2"),
];

const parallelPeerShippingEvents: A2AEventSSEPayload[] = [
    createEvent({
        direction: "request",
        timestamp: offsetTime(1200),
        task_id: "task-pp-3",
        source_entity: "Orchestrator",
        target_entity: "ShippingAgent",
        payload_summary: { method: "message/send" },
        full_payload: {
            method: "message/send",
            params: {
                message: {
                    parts: [{ kind: "text", text: "Get shipping estimate for order #9999" }],
                    metadata: { function_call_id: "fc-shipping-9", parentTaskId: "task-1", agent_name: "ShippingAgent" },
                },
                metadata: { function_call_id: "fc-shipping-9", parentTaskId: "task-1" },
            },
        },
    }),
    createSignalEvent("llm_invocation", { request: { model: "claude-sonnet-4-6", contents: [{ role: "user", parts: [{ text: "Get shipping estimate for order #9999" }] }] } }, "ShippingAgent", offsetTime(1500), "task-pp-3"),
    createSignalEvent("llm_response", { data: { content: { parts: [{ text: "Estimated 3 days via FedEx." }] } } }, "ShippingAgent", offsetTime(2500), "task-pp-3"),
    createCompletedResponseEvent("Estimated 3 days via FedEx.", "ShippingAgent", offsetTime(2800), "task-pp-3"),
];

const parallelPeerParentTask = createTask({
    taskId: "task-1",
    initialRequestText: "Check order #9999 validity and fetch shipping estimate",
    events: parallelPeerParentEvents,
    lastUpdated: new Date(offsetTime(4000)),
});

const parallelPeerValidatorTask = createTask({
    taskId: "task-pp-2",
    initialRequestText: "Validate order #9999",
    events: parallelPeerValidatorEvents,
    firstSeen: new Date(offsetTime(1200)),
    lastUpdated: new Date(offsetTime(2500)),
    parentTaskId: "task-1",
});

const parallelPeerShippingTask = createTask({
    taskId: "task-pp-3",
    initialRequestText: "Get shipping estimate for order #9999",
    events: parallelPeerShippingEvents,
    firstSeen: new Date(offsetTime(1200)),
    lastUpdated: new Date(offsetTime(2800)),
    parentTaskId: "task-1",
});

export const parallelPeerMonitoredTasks: Record<string, TaskFE> = {
    "task-1": parallelPeerParentTask,
    "task-pp-2": parallelPeerValidatorTask,
    "task-pp-3": parallelPeerShippingTask,
};

const workflowMapEvents: A2AEventSSEPayload[] = [
    createRequestEvent("Process all customer files through the review pipeline", offsetTime(0)),
    createSignalEvent(
        "workflow_execution_start",
        {
            workflow_name: "Customer Review Pipeline",
            execution_id: "exec-map-001",
            workflow_input: { files: ["file1.csv", "file2.csv", "file3.csv"] },
        },
        "Orchestrator",
        offsetTime(500)
    ),
    createSignalEvent(
        "workflow_node_execution_start",
        {
            node_id: "review-agent",
            node_type: "agent",
            agent_name: "ReviewAgent",
        },
        "Orchestrator",
        offsetTime(1000)
    ),
    createSignalEvent(
        "workflow_map_progress",
        {
            node_id: "review-agent",
            total_items: 3,
            completed_items: 1,
            status: "in-progress",
        },
        "Orchestrator",
        offsetTime(2000)
    ),
    createSignalEvent(
        "workflow_map_progress",
        {
            node_id: "review-agent",
            total_items: 3,
            completed_items: 2,
            status: "in-progress",
        },
        "Orchestrator",
        offsetTime(3000)
    ),
    createSignalEvent(
        "workflow_map_progress",
        {
            node_id: "review-agent",
            total_items: 3,
            completed_items: 3,
            status: "completed",
        },
        "Orchestrator",
        offsetTime(4000)
    ),
    createSignalEvent(
        "workflow_node_execution_result",
        {
            node_id: "review-agent",
            status: "success",
            metadata: { items_processed: 3, all_passed: true },
        },
        "Orchestrator",
        offsetTime(4500)
    ),
    createSignalEvent(
        "workflow_execution_result",
        {
            status: "success",
            workflow_output: { total_reviewed: 3, passed: 3, failed: 0 },
        },
        "Orchestrator",
        offsetTime(5000)
    ),
    createCompletedResponseEvent("All 3 customer files reviewed successfully.", "Orchestrator", offsetTime(5500)),
];

const workflowAgentRequestEvents: A2AEventSSEPayload[] = [
    createEvent({
        direction: "request",
        timestamp: offsetTime(1100),
        task_id: "task-wf-agent-1",
        source_entity: "Workflow",
        target_entity: "ReviewAgent",
        payload_summary: { method: "message/send" },
        full_payload: {
            method: "message/send",
            params: {
                message: {
                    parts: [
                        { kind: "text", text: "Review customer file file1.csv" },
                        {
                            kind: "data",
                            data: {
                                type: "structured_invocation_request",
                                input_schema: { type: "object", properties: { filename: { type: "string" } } },
                                output_schema: { type: "object", properties: { passed: { type: "boolean" } } },
                            },
                        },
                    ],
                    metadata: {
                        workflow_name: "Customer Review Pipeline",
                        node_id: "review-agent",
                        parentTaskId: "task-1",
                        agent_name: "ReviewAgent",
                    },
                },
                metadata: {
                    parentTaskId: "task-1",
                },
            },
        },
    }),
    createCompletedResponseEvent("Review complete. File passed.", "ReviewAgent", offsetTime(1900), "task-wf-agent-1"),
];

const workflowMapParentTask = createTask({
    taskId: "task-1",
    initialRequestText: "Process all customer files through the review pipeline",
    events: workflowMapEvents,
    lastUpdated: new Date(offsetTime(5500)),
});

const workflowAgentSubTask = createTask({
    taskId: "task-wf-agent-1",
    initialRequestText: "Review customer file file1.csv",
    events: workflowAgentRequestEvents,
    firstSeen: new Date(offsetTime(1100)),
    lastUpdated: new Date(offsetTime(1900)),
    parentTaskId: "task-1",
});

export const workflowMapMonitoredTasks: Record<string, TaskFE> = {
    "task-1": workflowMapParentTask,
    "task-wf-agent-1": workflowAgentSubTask,
};

const mixedParallelParentEvents: A2AEventSSEPayload[] = [
    createRequestEvent("Analyze Q1 sales data and produce a report with charts", offsetTime(0)),
    createSignalEvent(
        "llm_invocation",
        {
            request: {
                model: "claude-sonnet-4-6",
                contents: [{ role: "user", parts: [{ text: "Analyze Q1 sales data and produce a report with charts" }] }],
            },
        },
        "Orchestrator",
        offsetTime(200)
    ),
    createSignalEvent(
        "llm_response",
        {
            data: {
                content: {
                    parts: [
                        { function_call: { id: "fc-analysis-1", name: "peer_AnalysisAgent", args: { dataset: "Q1 sales" } } },
                        { function_call: { id: "fc-report-1", name: "peer_ReportAgent", args: { dataset: "Q1 sales", include_charts: true } } },
                    ],
                },
            },
        },
        "Orchestrator",
        offsetTime(1000)
    ),
    createSignalEvent("tool_invocation_start", { function_call_id: "fc-analysis-1", tool_name: "peer_AnalysisAgent", tool_args: { dataset: "Q1 sales" } }, "Orchestrator", offsetTime(1100)),
    createSignalEvent("tool_invocation_start", { function_call_id: "fc-report-1", tool_name: "peer_ReportAgent", tool_args: { dataset: "Q1 sales", include_charts: true } }, "Orchestrator", offsetTime(1100)),
    createSignalEvent("tool_result", { function_call_id: "fc-analysis-1", tool_name: "peer_AnalysisAgent", result_data: { trend: "upward", growth: "12%" } }, "Orchestrator", offsetTime(5000)),
    createSignalEvent("tool_result", { function_call_id: "fc-report-1", tool_name: "peer_ReportAgent", result_data: { report_url: "/reports/q1.pdf", charts: 3 } }, "Orchestrator", offsetTime(5500)),
    createStatusUpdateEvent("Analysis complete: Q1 shows 12% growth. Report generated with 3 charts.", "Orchestrator", offsetTime(6000)),
    createCompletedResponseEvent("Analysis complete: Q1 shows 12% growth. Report generated with 3 charts.", "Orchestrator", offsetTime(6500)),
];

const mixedParallelAnalysisEvents: A2AEventSSEPayload[] = [
    createEvent({
        direction: "request",
        timestamp: offsetTime(1200),
        task_id: "task-mix-analysis",
        source_entity: "Orchestrator",
        target_entity: "AnalysisAgent",
        payload_summary: { method: "message/send" },
        full_payload: {
            method: "message/send",
            params: {
                message: {
                    parts: [{ kind: "text", text: "Analyze Q1 sales data" }],
                    metadata: { function_call_id: "fc-analysis-1", parentTaskId: "task-1", agent_name: "AnalysisAgent" },
                },
                metadata: { function_call_id: "fc-analysis-1", parentTaskId: "task-1" },
            },
        },
    }),
    createSignalEvent("llm_invocation", { request: { model: "claude-sonnet-4-6", contents: [{ role: "user", parts: [{ text: "Analyze Q1 sales data" }] }] } }, "AnalysisAgent", offsetTime(1400), "task-mix-analysis"),
    createSignalEvent("llm_response", { data: { content: { parts: [{ function_call: { id: "fc-fetch-1", name: "fetch_data", args: { source: "sales_db", quarter: "Q1" } } }] } } }, "AnalysisAgent", offsetTime(1800), "task-mix-analysis"),
    createSignalEvent("tool_invocation_start", { function_call_id: "fc-fetch-1", tool_name: "fetch_data", tool_args: { source: "sales_db", quarter: "Q1" } }, "AnalysisAgent", offsetTime(1900), "task-mix-analysis"),
    createSignalEvent("tool_result", { function_call_id: "fc-fetch-1", tool_name: "fetch_data", result_data: { rows: 5000, columns: ["date", "amount", "region"] } }, "AnalysisAgent", offsetTime(2500), "task-mix-analysis"),
    createSignalEvent(
        "llm_invocation",
        {
            request: {
                model: "claude-sonnet-4-6",
                contents: [
                    { role: "user", parts: [{ text: "Analyze Q1 sales data" }] },
                    { role: "model", parts: [{ function_call: { id: "fc-fetch-1", name: "fetch_data" } }] },
                    { role: "user", parts: [{ function_response: { name: "fetch_data", response: { rows: 5000 } } }] },
                ],
            },
        },
        "AnalysisAgent",
        offsetTime(2600),
        "task-mix-analysis"
    ),
    createSignalEvent("llm_response", { data: { content: { parts: [{ function_call: { id: "fc-analyze-1", name: "analyze_data", args: { method: "trend_analysis" } } }] } } }, "AnalysisAgent", offsetTime(3000), "task-mix-analysis"),
    createSignalEvent("tool_invocation_start", { function_call_id: "fc-analyze-1", tool_name: "analyze_data", tool_args: { method: "trend_analysis" } }, "AnalysisAgent", offsetTime(3100), "task-mix-analysis"),
    createSignalEvent("tool_result", { function_call_id: "fc-analyze-1", tool_name: "analyze_data", result_data: { trend: "upward", growth_rate: "12%", top_region: "North America" } }, "AnalysisAgent", offsetTime(4000), "task-mix-analysis"),
    createSignalEvent("llm_response", { data: { content: { parts: [{ text: "Q1 sales show 12% upward trend. Top region: North America." }] } } }, "AnalysisAgent", offsetTime(4200), "task-mix-analysis"),
    createCompletedResponseEvent("Q1 sales show 12% upward trend. Top region: North America.", "AnalysisAgent", offsetTime(4500), "task-mix-analysis"),
];

const mixedParallelReportEvents: A2AEventSSEPayload[] = [
    createEvent({
        direction: "request",
        timestamp: offsetTime(1200),
        task_id: "task-mix-report",
        source_entity: "Orchestrator",
        target_entity: "ReportAgent",
        payload_summary: { method: "message/send" },
        full_payload: {
            method: "message/send",
            params: {
                message: {
                    parts: [{ kind: "text", text: "Generate report with charts for Q1 sales" }],
                    metadata: { function_call_id: "fc-report-1", parentTaskId: "task-1", agent_name: "ReportAgent" },
                },
                metadata: { function_call_id: "fc-report-1", parentTaskId: "task-1" },
            },
        },
    }),
    createSignalEvent("llm_invocation", { request: { model: "claude-sonnet-4-6", contents: [{ role: "user", parts: [{ text: "Generate report with charts for Q1 sales" }] }] } }, "ReportAgent", offsetTime(1400), "task-mix-report"),
    createSignalEvent(
        "llm_response",
        {
            data: {
                content: {
                    parts: [
                        { function_call: { id: "fc-chart-1", name: "generate_chart", args: { type: "bar", data: "Q1 sales by region" } } },
                        { function_call: { id: "fc-summary-1", name: "generate_summary", args: { format: "executive", quarter: "Q1" } } },
                    ],
                },
            },
        },
        "ReportAgent",
        offsetTime(1800),
        "task-mix-report"
    ),
    createSignalEvent(
        "tool_invocation_start",
        { function_call_id: "fc-chart-1", tool_name: "generate_chart", tool_args: { type: "bar", data: "Q1 sales by region" }, parallel_group_id: "pg-report-1" },
        "ReportAgent",
        offsetTime(1900),
        "task-mix-report"
    ),
    createSignalEvent(
        "tool_invocation_start",
        { function_call_id: "fc-summary-1", tool_name: "generate_summary", tool_args: { format: "executive", quarter: "Q1" }, parallel_group_id: "pg-report-1" },
        "ReportAgent",
        offsetTime(1900),
        "task-mix-report"
    ),
    createSignalEvent("tool_result", { function_call_id: "fc-chart-1", tool_name: "generate_chart", result_data: { chart_url: "/charts/q1_bar.png", type: "bar" } }, "ReportAgent", offsetTime(3000), "task-mix-report"),
    createSignalEvent("tool_result", { function_call_id: "fc-summary-1", tool_name: "generate_summary", result_data: { summary: "Q1 revenue up 12%, led by North America." } }, "ReportAgent", offsetTime(3200), "task-mix-report"),
    createSignalEvent("llm_response", { data: { content: { parts: [{ text: "Report generated with bar chart and executive summary." }] } } }, "ReportAgent", offsetTime(3800), "task-mix-report"),
    createCompletedResponseEvent("Report generated with bar chart and executive summary.", "ReportAgent", offsetTime(4000), "task-mix-report"),
];

const mixedParallelParentTask = createTask({
    taskId: "task-1",
    initialRequestText: "Analyze Q1 sales data and produce a report with charts",
    events: mixedParallelParentEvents,
    lastUpdated: new Date(offsetTime(6500)),
});

const mixedParallelAnalysisTask = createTask({
    taskId: "task-mix-analysis",
    initialRequestText: "Analyze Q1 sales data",
    events: mixedParallelAnalysisEvents,
    firstSeen: new Date(offsetTime(1200)),
    lastUpdated: new Date(offsetTime(4500)),
    parentTaskId: "task-1",
});

const mixedParallelReportTask = createTask({
    taskId: "task-mix-report",
    initialRequestText: "Generate report with charts for Q1 sales",
    events: mixedParallelReportEvents,
    firstSeen: new Date(offsetTime(1200)),
    lastUpdated: new Date(offsetTime(4000)),
    parentTaskId: "task-1",
});

export const mixedParallelMonitoredTasks: Record<string, TaskFE> = {
    "task-1": mixedParallelParentTask,
    "task-mix-analysis": mixedParallelAnalysisTask,
    "task-mix-report": mixedParallelReportTask,
};

// exported so tests can end the call with a different result, or none.

export const REPORT_TOOL_CALL_ID = "fc-report-1";

export const reportToolCallStartEvents: A2AEventSSEPayload[] = [
    createRequestEvent("Show me the quarterly report", offsetTime(0)),
    createSignalEvent("llm_invocation", { request: { model: "claude-sonnet-4-6", contents: [{ role: "user", parts: [{ text: "Show me the quarterly report" }] }] } }, "Orchestrator", offsetTime(200)),
    createSignalEvent("llm_response", { data: { content: { parts: [{ function_call: { id: REPORT_TOOL_CALL_ID, name: "get_report", args: { quarter: "Q3" } } }] } } }, "Orchestrator", offsetTime(1200)),
    createSignalEvent("tool_invocation_start", { function_call_id: REPORT_TOOL_CALL_ID, tool_name: "get_report", tool_args: { quarter: "Q3" } }, "Orchestrator", offsetTime(1300)),
];

export function reportToolResultEvent(resultData: unknown, timestamp: string): A2AEventSSEPayload {
    return createSignalEvent("tool_result", { function_call_id: REPORT_TOOL_CALL_ID, tool_name: "get_report", result_data: resultData }, "Orchestrator", timestamp);
}

export const reportToolSignInPlaceholderEvents: A2AEventSSEPayload[] = [
    reportToolResultEvent(
        {
            status: "pending",
            _pending_auth_marker: "<REDACTED>",
            _pending_auth_credential_key: "<REDACTED>",
            _pending_auth_function_call_id: REPORT_TOOL_CALL_ID,
            _pending_auth_follower: "<REDACTED>",
            _pending_auth_original_args: { quarter: "Q3" },
        },
        offsetTime(1305)
    ),
    createSignalEvent(
        "authentication_result",
        { auth_request_function_call_id: REPORT_TOOL_CALL_ID, credential_key: "cred-key-1", success: true, tool_display_name: "Reports", target_agent: "Orchestrator", target_agent_task_id: "task-1" },
        "Orchestrator",
        offsetTime(8000)
    ),
];

export const REPORT_TOOL_FINAL_RESULT_TIME = offsetTime(12000);

const pendingAuthToolCallEvents: A2AEventSSEPayload[] = [
    ...reportToolCallStartEvents,
    ...reportToolSignInPlaceholderEvents,
    reportToolResultEvent({ status: "error", error_code: "TOOL_EXECUTION_FAILED", message: "Report service rejected the request" }, REPORT_TOOL_FINAL_RESULT_TIME),
    createCompletedResponseEvent("I couldn't fetch the report.", "Orchestrator", offsetTime(13000)),
];

export const pendingAuthToolCallTask = createTask({
    taskId: "task-1",
    initialRequestText: "Show me the quarterly report",
    events: pendingAuthToolCallEvents,
    lastUpdated: new Date(offsetTime(13000)),
});

export const pendingAuthToolCallMonitoredTasks: Record<string, TaskFE> = {
    "task-1": pendingAuthToolCallTask,
};
