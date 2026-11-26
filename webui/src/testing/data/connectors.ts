// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import bedrockLogo from "@/features/connectors/assets/bedrock.svg";
import exaLogo from "@/features/connectors/assets/exa.svg";
import mariadbLogo from "@/features/connectors/assets/mariadb.svg";
import mssqlLogo from "@/features/connectors/assets/mssql.svg";
import mysqlLogo from "@/features/connectors/assets/mysql.svg";
import oracleLogo from "@/features/connectors/assets/oracle.svg";
import postgresLogo from "@/features/connectors/assets/postgresql.svg";
import slackLogo from "@/features/connectors/assets/slack.svg";
import tavilyLogo from "@/features/connectors/assets/tavily.svg";
import { SCHEMA_TYPE_NAME_MAP } from "@/features/connectors/hooks/useConnectorSchemas";
import { createFullConnectorSchemaValidator } from "@/features/connectors/utils/schemaGenerator";

import type { Connector, ConnectorFieldSchema, ConnectorFieldType, ConnectorSchema, ConnectorSubType, ConnectorSubtypeSchema, ConnectorType } from "@/types";
import { CONNECTOR_TYPE_MAP } from "@/types";

export const mockConnectors: Connector[] = [
    {
        id: "mock-connector-1",
        name: "PostgreSQL Connector",
        description: "A connector for PostgreSQL database for production purposes.",
        type: "sql",
        subtype: "postgres",
        values: {
            host: "localhost",
            port: 5432,
            name: "Dev DB",
            username: "testuser",
        },
        agentCount: 3,
    },
    {
        id: "mock-connector-2",
        name: "MySQL Connector",
        description: "A connector for MySQL database for testing purposes.",
        type: "sql",
        subtype: "mysql",
        values: {
            host: "localhost",
            port: 3306,
            name: "Dev DB",
            username: "testuser",
        },
        agentCount: 0,
    },
    {
        id: "mock-connector-3",
        name: "a".repeat(40),
        description: "b".repeat(1000),
        type: "sql",
        subtype: "postgres",
        values: {
            hostname: "c".repeat(255),
            port: 65535,
            username: "d".repeat(255),
            password: "longpasswordstringthatisalsolonglonglongandcanbeevenlonger",
            database: "e".repeat(255),
        },
        agentCount: 0,
    },
    {
        id: "mock-connector-4",
        name: "My AWS Bedrock Connector",
        description: "Connector for AWS Bedrock service.",
        type: "knowledge_base",
        subtype: "bedrock",
        values: {
            kb_id: "CIQYSFEKU3",
            region: "us-east-1",
            auth_type: "access_key",
            aws_access_key_id: "AKIAIOSFODNN7EXAMPLE",
            aws_secret_access_key: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
        },
        agentCount: 1,
    },
];

export const mockWebSearchConnector: Connector = {
    id: "mock-web-search-connector",
    name: "Team Web Search",
    description: "Cited web search for the research agents.",
    type: "web_search",
    subtype: "tavily",
    values: {
        api_key: "tvly-example-key",
        api_base: "https://api.tavily.com",
        max_results: 5,
        safe_search: "no",
    },
    agentCount: 0,
};

const createConnectorFieldSchema = (label: string, type: ConnectorFieldType, order: number, options: Partial<Omit<ConnectorFieldSchema, "label" | "type" | "order">> = {}): ConnectorFieldSchema => ({
    label,
    type,
    order,
    required: false,
    secret: false,
    ...options,
});

const createHostField = (order: number = 1): ConnectorFieldSchema => ({
    label: "Host",
    type: "string",
    required: true,
    secret: false,
    hint: "Database hostname or IP address",
    order,
    validation: {
        minLength: 1,
        maxLength: 255,
        pattern: "^[a-zA-Z0-9.-]+$",
    },
});

const createPortField = (defaultPort: number, order: number = 2): ConnectorFieldSchema => ({
    label: "Port",
    type: "number",
    required: false,
    secret: false,
    default: defaultPort,
    placeholder: defaultPort.toString(),
    order,
    validation: {
        min: 1,
        max: 65535,
    },
});

const createUsernameField = (order: number = 4): ConnectorFieldSchema => ({
    label: "Username",
    type: "string",
    required: true,
    secret: false,
    order,
    validation: {
        minLength: 1,
        maxLength: 255,
    },
});

const createPasswordField = (order: number = 5): ConnectorFieldSchema => ({
    label: "Password",
    type: "password",
    required: true,
    secret: true,
    hint: "This value will not be shown after creation",
    order,
    validation: {
        minLength: 1,
    },
});

const createDatabaseNameField = (label: string = "Name", order: number = 3): ConnectorFieldSchema => ({
    label,
    type: "string",
    required: true,
    secret: false,
    order,
    validation: {
        minLength: 1,
        maxLength: 255,
    },
});

const createConnectorSchema = (
    subtype: ConnectorSubType,
    name: string,
    description: string,
    fields: Record<string, ConnectorFieldSchema>,
    options: {
        connectionTemplate?: string;
        warning?: string;
    } = {}
): ConnectorSubtypeSchema => ({
    subtype,
    name,
    description,
    fieldSchema: fields,
    ...options,
});

const commonSqlFields = {
    username: createUsernameField(4),
    password: createPasswordField(5),
};

const postgresFields: Record<string, ConnectorFieldSchema> = {
    host: createHostField(1),
    port: createPortField(5432, 2),
    name: createDatabaseNameField("Name", 3),
    ...commonSqlFields,
};

const mysqlFields: Record<string, ConnectorFieldSchema> = {
    name: createDatabaseNameField("Database Name", 1),
    host: createHostField(2),
    port: createPortField(3306, 3),
    ...commonSqlFields,
};

const mariadbFields: Record<string, ConnectorFieldSchema> = {
    name: createDatabaseNameField("Database Name", 1),
    host: createHostField(2),
    port: createPortField(3306, 3),
    ...commonSqlFields,
};

const bedrockFields: Record<string, ConnectorFieldSchema> = {
    kb_id: createConnectorFieldSchema("Knowledge Base ID", "string", 1, {
        required: true,
        placeholder: "ABC123XYZ",
        hint: "",
        validation: {
            minLength: 1,
            maxLength: 100,
        },
    }),
    region: createConnectorFieldSchema("AWS Region", "string", 2, {
        required: true,
        default: "us-east-1",
        placeholder: "us-east-1",
        hint: "",
        validation: {
            minLength: 1,
            maxLength: 50,
            pattern: "^[a-z0-9]+(-[a-z0-9]+)*$",
            errorMsg: "AWS Region must be a valid region name (e.g., us-east-1, eu-west-1)",
        },
    }),
    auth_type: createConnectorFieldSchema("Authentication Scheme", "select", 3, {
        required: true,
        default: "access_key",
        hint: "Authentication method for connecting to Amazon Bedrock Knowledge Base. AWS IAM Role Chaining is only supported when SAM runs on AWS.",
        options: [
            { value: "iam", label: "AWS IAM Role Chaining" },
            { value: "access_key", label: "AWS Access Key" },
        ],
    }),
    aws_access_key_id: createConnectorFieldSchema("AWS Access Key", "string", 4, {
        required: true,
        hint: "",
        validation: {
            minLength: 16,
            maxLength: 128,
        },
        conditionalDisplay: {
            auth_type: "access_key",
        },
    }),
    aws_secret_access_key: createConnectorFieldSchema("AWS Secret Key", "password", 5, {
        required: true,
        secret: true,
        hint: "The Secret Key associated with the Access Key ID",
        validation: {
            minLength: 30,
            maxLength: 255,
        },
        conditionalDisplay: {
            auth_type: "access_key",
        },
    }),
    aws_account_id: createConnectorFieldSchema("AWS Account ID", "string", 6, {
        required: true,
        placeholder: "123456789012",
        hint: "The AWS Account ID where the Bedrock Knowledge Base is located. Required for IAM role assumption.",
        validation: {
            minLength: 1,
            maxLength: 20,
            pattern: "^\\d+$",
        },
        conditionalDisplay: {
            auth_type: "iam",
        },
    }),
    role_name: createConnectorFieldSchema("Role Name", "string", 7, {
        required: true,
        placeholder: "SolaceBedrockKBAccess",
        hint: "Name of the IAM role with permissions to access the Bedrock Knowledge Base (e.g., 'BedrockKBAccessRole')",
        validation: {
            minLength: 1,
            maxLength: 64,
        },
        conditionalDisplay: {
            auth_type: "iam",
        },
    }),
    session_name: createConnectorFieldSchema("Session Name", "string", 8, {
        required: false,
        placeholder: "solace-kb-session",
        hint: "Session name for auditing the assumed role in AWS CloudTrail logs. Defaults to 'solace-kb-session'.",
        validation: {
            minLength: 2,
            maxLength: 64,
        },
        conditionalDisplay: {
            auth_type: "iam",
        },
    }),
    external_id: createConnectorFieldSchema("External ID", "string", 9, {
        required: false,
        placeholder: "solace-sam-abc-123",
        hint: "Optional security token for cross-account access. Required if configured in the IAM role's trust policy.",
        validation: {
            minLength: 2,
            maxLength: 1224,
        },
        conditionalDisplay: {
            auth_type: "iam",
        },
    }),
};

const slackFields: Record<string, ConnectorFieldSchema> = {
    slack_bot_token: createConnectorFieldSchema("Bot Token", "password", 1, {
        required: true,
        secret: true,
        placeholder: "xoxb-...",
        hint: "Slack Bot User OAuth Token. Recommended scopes: chat:write, files:write, users:read, users:read.email",
        validation: {
            minLength: 10,
            maxLength: 255,
        },
    }),
};

const createWebSearchFields = (provider: string, apiBase: string, filtered: string): Record<string, ConnectorFieldSchema> => ({
    api_key: createConnectorFieldSchema("API Key", "password", 1, {
        required: true,
        secret: true,
        hint: `Your ${provider} API key. Leave the placeholder to keep an existing value.`,
        validation: {
            minLength: 1,
            maxLength: 1024,
        },
    }),
    api_base: createConnectorFieldSchema("API Base URL", "string", 2, {
        required: true,
        default: apiBase,
        placeholder: apiBase,
        hint: `${provider}'s API endpoint. Change it only to route searches through a private mirror or proxy.`,
        validation: {
            maxLength: 2048,
            pattern: "^https?://[^\\s/?#]+[^\\s?#]*$",
            errorMsg: "API Base URL must be an http:// or https:// URL with a host",
        },
    }),
    max_results: createConnectorFieldSchema("Results per Search", "number", 3, {
        default: 5,
        placeholder: "5",
        hint: "How many results a search returns when the agent does not ask for a number.",
        validation: {
            min: 1,
            max: 20,
        },
    }),
    safe_search: createConnectorFieldSchema("Safe Search", "select", 4, {
        default: "no",
        hint: `Filter ${filtered} content out of the results.`,
        options: [
            { value: "no", label: "No", description: "Return results without content filtering" },
            { value: "yes", label: "Yes", description: `Filter ${filtered} content out of the results` },
        ],
    }),
    hil: createConnectorFieldSchema("Tool Approvals", "hil", 900, {
        hidden: true,
        hint: "Approval gates for this connector's tools, keyed by role (`web_search`). `{}` removes them.",
    }),
});

const createAPIConnectorSchema = (subtype: ConnectorSubType, name: string): ConnectorSubtypeSchema => ({
    subtype,
    name,
    description: "API Connector",
    fieldSchema: {
        file_s3_key: createConnectorFieldSchema("OpenAPI Specification File S3 Key", "string", -1, {
            required: false,
            secret: true,
        }),
        base_url: createConnectorFieldSchema("Base URL", "string", 2, {
            required: true,
            hint: "Select the base URL from the OpenAPI specification",
        }),
        allow_list: createConnectorFieldSchema("Allowed Tools", "string", 3, {
            required: false,
            hint: "Select which API operations to expose as tools",
        }),
    },
});

export const mockConnectorsSchemaMap = new Map([
    ["sql-postgres", createConnectorSchema("postgres", "PostgreSQL", "SQL database", postgresFields)],
    ["sql-mysql", createConnectorSchema("mysql", "MySQL", "SQL database", mysqlFields)],
    ["sql-mariadb", createConnectorSchema("mariadb", "MariaDB", "SQL database", mariadbFields)],
    [
        "knowledge_base-bedrock",
        createConnectorSchema("bedrock", "Amazon Bedrock", "Connect to Amazon Bedrock Knowledge Base for RAG retrieval", bedrockFields, {
            warning: "All agents using this connector will have access to the same Knowledge Base. Access control must be configured at the AWS IAM level. For AWS IAM Role authentication, ensure SAM is deployed on AWS.",
        }),
    ],
    ["api-openapi", createAPIConnectorSchema("openapi", "OpenAPI")],
    ["slack-bot", createConnectorSchema("bot", "Slack", "Send messages to Slack channels", slackFields)],
    [
        "web_search-tavily",
        createConnectorSchema("tavily", "Tavily", "Search the web with Tavily and cite the sources", createWebSearchFields("Tavily", "https://api.tavily.com", "adult"), {
            warning: "Tavily bills each search to this API key, and every agent that uses this connector shares the key and its plan limits.",
        }),
    ],
    [
        "web_search-exa",
        createConnectorSchema("exa", "Exa", "Search the web with Exa and cite the sources", createWebSearchFields("Exa", "https://api.exa.ai", "unsafe"), {
            warning: "Exa bills each search to this API key, and every agent that uses this connector shares the key and its credits.",
        }),
    ],
]);

const SCHEMA_TYPE_ICON_MAP: Record<string, Record<string, string>> = {
    sql: {
        postgres: postgresLogo,
        mysql: mysqlLogo,
        mariadb: mariadbLogo,
        mssql: mssqlLogo,
        oracle: oracleLogo,
    },
    knowledge_base: {
        bedrock: bedrockLogo,
    },
    slack: {
        bot: slackLogo,
    },
    web_search: {
        tavily: tavilyLogo,
        exa: exaLogo,
    },
};

export const mockGetConnectorSchema = (type: string, subtype: string): ConnectorSchema | undefined => {
    const key = `${type}-${subtype}`;
    const connectorData = mockConnectorsSchemaMap.get(key);

    if (!connectorData) {
        return undefined;
    }

    const schemaInfo: ConnectorSchema = {
        ...connectorData,
        type: type as ConnectorType,
        schemaValidator: createFullConnectorSchemaValidator(connectorData, {}, false),
        icon: SCHEMA_TYPE_ICON_MAP[type]?.[subtype],
    };

    return schemaInfo;
};

export const mockConnectorSchemas: ConnectorSchema[] = Array.from(mockConnectorsSchemaMap.entries())
    .map(([key]) => {
        const [type, subtype] = key.split("-");
        const schema = mockGetConnectorSchema(type, subtype)!;
        return type === "event_mesh" ? schema : { ...schema, name: `${schema.name} | ${SCHEMA_TYPE_NAME_MAP[CONNECTOR_TYPE_MAP[schema.type]]}` };
    })
    .sort((a, b) => a.name.localeCompare(b.name));

const createSqlConnector = (id: string | number, overrides: Partial<Connector> = {}): Connector => {
    const defaultConnector: Connector = {
        id: `mock-sql-connector-${id}`,
        name: `MySQL Connector ${id}`,
        description: "A connector for MySQL database for testing purposes.",
        type: "sql",
        subtype: "mysql",
        values: {
            host: "localhost",
            port: 3306,
            database: "Dev DB",
            username: "testuser",
        },
        agentCount: 0,
    };

    return {
        ...defaultConnector,
        ...overrides,
        type: "sql",
        values: {
            ...defaultConnector.values,
            ...(overrides.values || {}),
        },
    };
};

export const createConnectors = (count: number): Connector[] => {
    const connectors = [];

    for (let i = 0; i <= count; i++) {
        connectors.push(createSqlConnector(i.toString()));
    }
    return connectors;
};
