// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { WorkflowConfig } from "@/utils/agentUtils";

export const completeOrderWorkflowConfig: WorkflowConfig = {
    description: "A comprehensive order processing workflow",
    input_schema: {
        type: "object",
        properties: {
            order_id: { type: "string", description: "Unique order identifier" },
            customer_id: { type: "string", description: "Customer identifier" },
            items: {
                type: "array",
                description: "Order items",
                items: {
                    type: "object",
                    properties: {
                        product_id: { type: "string" },
                        quantity: { type: "number" },
                    },
                },
            },
            shipping_priority: { type: "string", enum: ["express", "standard", "economy"] },
        },
        required: ["order_id", "customer_id"],
    },
    output_schema: {
        type: "object",
        properties: {
            order_id: { type: "string", description: "Order identifier" },
            status: { type: "string", description: "Order processing status" },
            customer_name: { type: "string", description: "Customer name" },
            final_total: { type: "number", description: "Final order total" },
            shipping_method: { type: "string", description: "Selected shipping method" },
        },
    },
    output_mapping: {
        order_id: "{{workflow.input.order_id}}",
        status: "{{finalize_order.output.status}}",
        customer_name: "{{enrich_customer.output.name}}",
        final_total: "{{calculate_price.output.final_total}}",
        shipping_method: "{{workflow.input.shipping_priority}}",
    },
    nodes: [
        { id: "validate_order", type: "agent", agent_name: "validate_order" },
        { id: "enrich_customer", type: "agent", agent_name: "enrich_customer" },
        { id: "calculate_price", type: "agent", agent_name: "calculate_price" },
        { id: "check_inventory", type: "agent", agent_name: "check_inventory" },
        { id: "process_shipping", type: "agent", agent_name: "process_shipping" },
        { id: "finalize_order", type: "agent", agent_name: "finalize_order" },
    ],
};
