// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import asanaIcon from "../../connectors/assets/asana.svg";
import awsIcon from "../../connectors/assets/aws.svg";
import azureIcon from "../../connectors/assets/azure.svg";
import bitbucketIcon from "../../connectors/assets/bitbucket.svg";
import confluenceIcon from "../../connectors/assets/confluence.svg";
import datadogIcon from "../../connectors/assets/datadog.svg";
import elasticsearchIcon from "../../connectors/assets/elasticsearch.svg";
import githubIcon from "../../connectors/assets/github.svg";
import gitlabIcon from "../../connectors/assets/gitlab.svg";
import googleIcon from "../../connectors/assets/google.svg";
import hubspotIcon from "../../connectors/assets/hubspot.svg";
import jiraIcon from "../../connectors/assets/jira.svg";
import linearIcon from "../../connectors/assets/linear.svg";
import microsoftGraphIcon from "../../connectors/assets/microsoft_graph.svg";
import mongodbIcon from "../../connectors/assets/mongodb.svg";
import mysqlIcon from "../../connectors/assets/mysql.svg";
import notionIcon from "../../connectors/assets/notion.svg";
import pagerdutyIcon from "../../connectors/assets/pagerduty.svg";
import postgresqlIcon from "../../connectors/assets/postgresql.svg";
import redisIcon from "../../connectors/assets/redis.svg";
import salesforceIcon from "../../connectors/assets/salesforce.svg";
import servicenowIcon from "../../connectors/assets/servicenow.svg";
import shopifyIcon from "../../connectors/assets/shopify.svg";
import slackIcon from "../../connectors/assets/slack.svg";
import snowflakeIcon from "../../connectors/assets/snowflake.svg";
import stripeIcon from "../../connectors/assets/stripe.svg";
import trelloIcon from "../../connectors/assets/trello.svg";
import twilioIcon from "../../connectors/assets/twilio.svg";
import webSearchIcon from "../../connectors/assets/web_search.svg";
import zendeskIcon from "../../connectors/assets/zendesk.svg";

const SERVICE_ICONS: Record<string, string> = {
    jira: jiraIcon,
    confluence: confluenceIcon,
    slack: slackIcon,
    github: githubIcon,
    gitlab: gitlabIcon,
    salesforce: salesforceIcon,
    servicenow: servicenowIcon,
    pagerduty: pagerdutyIcon,
    zendesk: zendeskIcon,
    stripe: stripeIcon,
    twilio: twilioIcon,
    notion: notionIcon,
    hubspot: hubspotIcon,
    trello: trelloIcon,
    asana: asanaIcon,
    linear: linearIcon,
    bitbucket: bitbucketIcon,
    mongodb: mongodbIcon,
    elasticsearch: elasticsearchIcon,
    snowflake: snowflakeIcon,
    shopify: shopifyIcon,
    datadog: datadogIcon,
    microsoft_graph: microsoftGraphIcon,
    gmail: googleIcon,
    google: googleIcon,
    aws: awsIcon,
    azure: azureIcon,
    redis: redisIcon,
    postgresql: postgresqlIcon,
    mysql: mysqlIcon,
    web_search: webSearchIcon,
};

export function lookupConnectorIconByKey(iconKey: string): string | undefined {
    return SERVICE_ICONS[iconKey];
}

export function inferConnectorIconFromName(componentName: string): string | undefined {
    const normalized = componentName.toLowerCase().replace(/[-_\s]/g, "_");
    for (const key of Object.keys(SERVICE_ICONS)) {
        if (normalized.includes(key)) {
            return SERVICE_ICONS[key];
        }
    }
    return undefined;
}
