// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { connectorsApi } from "@/api/connectors/versioned";

import { CONNECTOR_TYPE_MAP, type ConnectorSchema, type ConnectorSubType, type ConnectorSubtypeSchema, ConnectorType } from "@/types";

import bedrock from "../assets/bedrock.svg";
import dynamodbLogo from "../assets/dynamodb.svg";
import elasticsearchLogo from "../assets/elasticsearch.svg";
import emailLogo from "../assets/email.svg";
import exaLogo from "../assets/exa.svg";
import mariadbLogo from "../assets/mariadb.svg";
import mcpLogo from "../assets/mcp.svg";
import mongodbLogo from "../assets/mongodb.svg";
import mssqlLogo from "../assets/mssql.svg";
import mysqlLogo from "../assets/mysql.svg";
import neo4jLogo from "../assets/neo4j.svg";
import neptuneLogo from "../assets/neptune.svg";
import opensearchLogo from "../assets/opensearch.svg";
import oracleLogo from "../assets/oracle.svg";
import postgresLogo from "../assets/postgresql.svg";
import slackLogo from "../assets/slack.svg";
import solaceLogo from "../assets/solace.svg";
import tavilyLogo from "../assets/tavily.svg";
import { createFullConnectorSchemaValidator } from "../utils/schemaGenerator";

export const SCHEMA_TYPE_NAME_MAP: Record<string, string> = {
    [CONNECTOR_TYPE_MAP[ConnectorType.Sql]]: "SQL Database",
    [CONNECTOR_TYPE_MAP[ConnectorType.KnowledgeBase]]: "Knowledge Base",
    [CONNECTOR_TYPE_MAP[ConnectorType.Api]]: "API",
    [CONNECTOR_TYPE_MAP[ConnectorType.Mcp]]: "MCP",
    [CONNECTOR_TYPE_MAP[ConnectorType.DocumentDb]]: "Document Database",
    [CONNECTOR_TYPE_MAP[ConnectorType.GraphDb]]: "Graph Database",
    [CONNECTOR_TYPE_MAP[ConnectorType.Search]]: "Search",
    [CONNECTOR_TYPE_MAP[ConnectorType.WebSearch]]: "Web Search",
    [CONNECTOR_TYPE_MAP[ConnectorType.Slack]]: "Slack",
    [CONNECTOR_TYPE_MAP[ConnectorType.Email]]: "Email",
    [CONNECTOR_TYPE_MAP[ConnectorType.EventMesh]]: "Event Mesh",
};

const SCHEMA_TYPE_ICON_MAP: Record<string, Record<string, string>> = {
    [CONNECTOR_TYPE_MAP[ConnectorType.Sql]]: {
        postgres: postgresLogo,
        mysql: mysqlLogo,
        mariadb: mariadbLogo,
        mssql: mssqlLogo,
        oracle: oracleLogo,
    },
    [CONNECTOR_TYPE_MAP[ConnectorType.KnowledgeBase]]: {
        bedrock: bedrock,
    },
    [CONNECTOR_TYPE_MAP[ConnectorType.Api]]: {
        openapi: "plug",
    },
    [CONNECTOR_TYPE_MAP[ConnectorType.Mcp]]: {
        remote: mcpLogo,
    },
    [CONNECTOR_TYPE_MAP[ConnectorType.DocumentDb]]: {
        mongodb: mongodbLogo,
        dynamodb: dynamodbLogo,
    },
    [CONNECTOR_TYPE_MAP[ConnectorType.GraphDb]]: {
        neo4j: neo4jLogo,
        neptune: neptuneLogo,
    },
    [CONNECTOR_TYPE_MAP[ConnectorType.Search]]: {
        elasticsearch: elasticsearchLogo,
        opensearch: opensearchLogo,
    },
    [CONNECTOR_TYPE_MAP[ConnectorType.WebSearch]]: {
        tavily: tavilyLogo,
        exa: exaLogo,
    },
    [CONNECTOR_TYPE_MAP[ConnectorType.Slack]]: {
        bot: slackLogo,
    },
    [CONNECTOR_TYPE_MAP[ConnectorType.Email]]: {
        smtp: emailLogo,
    },
    [CONNECTOR_TYPE_MAP[ConnectorType.EventMesh]]: {
        solace: solaceLogo,
    },
};

interface UseConnectorSchemasReturn {
    connectorSchemas: ConnectorSchema[];
    getConnectorSchema(type: string, subtype: string): ConnectorSchema | undefined;
    refetch: () => Promise<void>;
}

export const useConnectorSchemas = ({ enabled }: { enabled: boolean }): UseConnectorSchemasReturn => {
    const [schemaCache] = useState<Map<string, ConnectorSchema>>(new Map());
    const [connectorSchemasMap, setConnectorSchemasMap] = useState<Map<string, ConnectorSubtypeSchema>>(new Map());
    const isFetchingRef = useRef(false);

    const refetch = useCallback(async () => {
        if (isFetchingRef.current || !enabled) return;

        isFetchingRef.current = true;

        try {
            const responseJson = await connectorsApi.getSchemas();
            const newTypeSubtypeMap = new Map<string, ConnectorSubtypeSchema>();

            responseJson.data.forEach(typeData => {
                typeData.subtypes.forEach(subtypeData => {
                    const key = `${typeData.type}-${subtypeData.subtype}`;
                    newTypeSubtypeMap.set(key, subtypeData);
                });
            });

            setConnectorSchemasMap(newTypeSubtypeMap);
            schemaCache.clear();
        } catch (err: unknown) {
            console.error("Error fetching connector schemas:", err);
            setConnectorSchemasMap(new Map());
        } finally {
            isFetchingRef.current = false;
        }
    }, [enabled]);

    useEffect(() => {
        refetch();
    }, [refetch]);

    const getConnectorSchema = useCallback(
        (type: ConnectorType, subtype: ConnectorSubType): ConnectorSchema | undefined => {
            const key = `${type}-${subtype}`;

            if (schemaCache.has(key)) {
                return schemaCache.get(key);
            }

            const connectorSchema = connectorSchemasMap.get(key);
            if (!connectorSchema) {
                return undefined;
            }

            const schemaInfo: ConnectorSchema = {
                ...connectorSchema,
                type,
                schemaValidator: createFullConnectorSchemaValidator(connectorSchema),
                name: type === ConnectorType.EventMesh ? connectorSchema?.name : connectorSchema?.name + " | " + SCHEMA_TYPE_NAME_MAP[CONNECTOR_TYPE_MAP[type]],
                icon: SCHEMA_TYPE_ICON_MAP[CONNECTOR_TYPE_MAP[type]]?.[subtype],
            };

            schemaCache.set(key, schemaInfo);

            return schemaInfo;
        },
        [connectorSchemasMap, schemaCache]
    );

    return useMemo(() => {
        const schemas: ConnectorSchema[] = [];

        connectorSchemasMap.forEach((_connectorSchema, key) => {
            const [type, subtype] = key.split("-");
            const schema = getConnectorSchema(type as ConnectorType, subtype as ConnectorSubType);
            if (schema) {
                schemas.push(schema);
            }
        });

        schemas.sort((a: ConnectorSchema, b: ConnectorSchema) => a.name.localeCompare(b.name));
        const connectorSchemas = schemas;

        return {
            connectorSchemas,
            getConnectorSchema,
            refetch,
        };
    }, [connectorSchemasMap, getConnectorSchema, refetch]);
};
