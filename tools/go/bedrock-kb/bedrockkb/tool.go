// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bedrockkb

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	bardoc "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/document"
	bartypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

var exitOnConfigError = func(err error) {
	fmt.Fprintf(os.Stderr, "bedrock-kb: invalid tool_config: %v\n", err)
	os.Exit(1)
}

type QueryParams struct {
	Query      string `json:"query" desc:"Search query to retrieve relevant documents from the knowledge base."`
	MaxResults *int   `json:"max_results" desc:"Number of results to return (1-20). Defaults to configured value."`
}

const baseInstructions = `This tool retrieves relevant documents from an Amazon Bedrock Knowledge Base using semantic search.

## Usage Guidelines
- Provide a clear, specific search query for best results
- Use max_results to control how many document chunks are returned
- Results include relevance scores — higher scores indicate better matches
- Use the retrieved context to ground your answers in source documents`

var bedrockKBConfigSchema = []sdk.ConfigSchemaField{
	{
		Key:         "kb_id",
		Type:        "string",
		Description: "Bedrock Knowledge Base identifier (e.g. \"ABCD1234EF\").",
		Required:    true,
	},
	{
		Key:         "region",
		Type:        "string",
		Description: "AWS region hosting the Knowledge Base.",
		Required:    true,
		Default:     "us-east-1",
	},
	{
		Key:         "auth_type",
		Type:        "string",
		Description: "Authentication mode: \"iam\" (IAM role on host) or \"access_key\" (static credentials).",
		Options:     []any{"iam", "access_key"},
		Default:     "iam",
	},
	{
		Key:         "aws_access_key_id",
		Type:        "string",
		Description: "AWS access key ID. Required when auth_type is access_key.",
		Secret:      true,
	},
	{
		Key:         "aws_secret_access_key",
		Type:        "string",
		Description: "AWS secret access key. Required when auth_type is access_key.",
		Secret:      true,
	},
	{
		Key:         "role_arn",
		Type:        "string",
		Description: "Optional IAM role ARN to assume before calling Bedrock (role chaining).",
	},
	{
		Key:         "external_id",
		Type:        "string",
		Description: "Optional external ID used with role_arn for cross-account AssumeRole.",
		Secret:      true,
	},
	{
		Key:         "session_name",
		Type:        "string",
		Description: "Optional STS session name used with role_arn.",
	},
	{
		Key:         "max_results",
		Type:        "integer",
		Description: "Default number of results when the LLM does not pass max_results (1-20).",
		Default:     5,
	},
}

func validateConfig(config map[string]any) error {
	kbID, _ := config["kb_id"].(string)
	if kbID == "" {
		return fmt.Errorf("kb_id is required")
	}
	region, _ := config["region"].(string)
	if region == "" {
		return fmt.Errorf("region is required")
	}
	authType, _ := config["auth_type"].(string)
	if authType == "" {
		authType = "iam"
	}
	if authType != "iam" && authType != "access_key" {
		return fmt.Errorf("auth_type must be \"iam\" or \"access_key\", got %q", authType)
	}
	if authType == "access_key" {
		if v, _ := config["aws_access_key_id"].(string); v == "" {
			return fmt.Errorf("aws_access_key_id is required when auth_type is access_key")
		}
		if v, _ := config["aws_secret_access_key"].(string); v == "" {
			return fmt.Errorf("aws_secret_access_key is required when auth_type is access_key")
		}
	}
	return nil
}

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("query_bedrock_kb",
			"Search Amazon Bedrock Knowledge Base for relevant documents and information",
			queryKB,
			sdk.WithInstructions(baseInstructions),
			sdk.WithConfigSchema(bedrockKBConfigSchema...),
			sdk.WithDynamicSchema(dynamicSchema),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
	}
}

func dynamicSchema(config map[string]any) *sdk.SchemaOverride {
	if err := validateConfig(config); err != nil {
		exitOnConfigError(err)
		return nil
	}

	kbID := configString(config, "kb_id", "")
	if kbID == "" {
		return nil
	}

	override := &sdk.SchemaOverride{}

	if name := configString(config, "tool_name", ""); name != "" {
		override.Name = name
	}
	if desc := configString(config, "tool_description", ""); desc != "" {
		override.Description = desc
	}

	auth := authFromConfig(config)
	clients, err := newBedrockClients(context.Background(), auth)
	if err != nil {
		override.Instructions = baseInstructions + criticalUnavailableNotice(err)
		return override
	}

	result, err := clients.agent.GetKnowledgeBase(context.Background(), &bedrockagent.GetKnowledgeBaseInput{
		KnowledgeBaseId: &kbID,
	})
	if err != nil {
		override.Instructions = baseInstructions + criticalUnavailableNotice(err)
		return override
	}

	kbName := ""
	kbDesc := ""
	if result.KnowledgeBase != nil {
		if result.KnowledgeBase.Name != nil {
			kbName = *result.KnowledgeBase.Name
		}
		if result.KnowledgeBase.Description != nil {
			kbDesc = *result.KnowledgeBase.Description
		}
	}

	status := fmt.Sprintf("\n\n## Knowledge Base Info\n- **ID**: %s\n- **Region**: %s\n- **Status**: Connected", kbID, auth.region)
	if kbName != "" {
		status += fmt.Sprintf("\n- **Name**: %s", kbName)
	}
	if kbDesc != "" {
		status += fmt.Sprintf("\n- **Description**: %s", kbDesc)
	}

	override.Instructions = baseInstructions + status
	return override
}

const criticalRetrieveFailureMsg = "CRITICAL: Failed to retrieve from Knowledge Base. Inform the user that the Knowledge Base is currently unavailable (credentials may have expired or been revoked) and they should check the connector configuration."

func criticalUnavailableNotice(err error) string {
	return "\n\nCRITICAL: This Knowledge Base tool is UNAVAILABLE.\nDO NOT attempt to use this tool. Immediately inform the user that the Knowledge Base is not accessible and they should check the connector configuration.\n\nDetails: " + err.Error()
}

func queryKB(ctx context.Context, p QueryParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	kbID := tc.GetConfigString("kb_id", "")
	if kbID == "" {
		return sdk.Error("CRITICAL: Knowledge Base tool is not available. Inform the user that the Knowledge Base cannot be accessed due to a configuration issue (kb_id not configured in tool_config)."), nil
	}

	auth := authFromToolContext(tc)
	client, err := newRuntimeClient(ctx, auth)
	if err != nil {
		return sdk.Error(fmt.Sprintf("%s (cause: %v)", criticalRetrieveFailureMsg, err)), nil
	}

	maxResults := configInt(configFromToolContext(tc), "max_results", 5)
	if p.MaxResults != nil {
		maxResults = *p.MaxResults
	}
	if maxResults < 1 {
		maxResults = 1
	}
	if maxResults > 20 {
		maxResults = 20
	}
	numResults := int32(maxResults)

	_ = tc.SendStatus("Searching knowledge base...")

	result, err := client.Retrieve(ctx, &bedrockagentruntime.RetrieveInput{
		KnowledgeBaseId: &kbID,
		RetrievalQuery: &bartypes.KnowledgeBaseQuery{
			Text: &p.Query,
		},
		RetrievalConfiguration: &bartypes.KnowledgeBaseRetrievalConfiguration{
			VectorSearchConfiguration: &bartypes.KnowledgeBaseVectorSearchConfiguration{
				NumberOfResults: &numResults,
			},
		},
	})
	if err != nil {
		return sdk.Error(fmt.Sprintf("%s (cause: %v)", criticalRetrieveFailureMsg, err)), nil
	}

	retrievalResults := make([]map[string]any, 0, len(result.RetrievalResults))
	for _, r := range result.RetrievalResults {
		retrievalResults = append(retrievalResults, buildRetrievalEntry(r))
	}

	return sdk.OK(
		fmt.Sprintf("Retrieved %d results from knowledge base", len(retrievalResults)),
		sdk.WithData(map[string]any{
			"retrieval_results": retrievalResults,
		}),
	), nil
}

func buildRetrievalEntry(r bartypes.KnowledgeBaseRetrievalResult) map[string]any {
	entry := map[string]any{}

	if content := buildContent(r.Content); len(content) > 0 {
		entry["content"] = content
	}
	if loc := buildLocation(r.Location); len(loc) > 0 {
		entry["location"] = loc
	}
	if meta := buildMetadata(r.Metadata); len(meta) > 0 {
		entry["metadata"] = meta
	}
	if r.Score != nil {
		entry["score"] = *r.Score
	}

	return entry
}

func buildContent(c *bartypes.RetrievalResultContent) map[string]any {
	if c == nil {
		return nil
	}
	content := map[string]any{}
	if c.Text != nil {
		content["text"] = *c.Text
	}
	if c.ByteContent != nil {
		content["byteContent"] = *c.ByteContent
	}
	if string(c.Type) != "" {
		content["type"] = string(c.Type)
	}
	return content
}

func buildLocation(l *bartypes.RetrievalResultLocation) map[string]any {
	if l == nil {
		return nil
	}
	loc := map[string]any{}
	if string(l.Type) != "" {
		loc["type"] = string(l.Type)
	}
	if l.S3Location != nil && l.S3Location.Uri != nil {
		loc["s3Location"] = map[string]any{"uri": *l.S3Location.Uri}
	}
	if l.ConfluenceLocation != nil && l.ConfluenceLocation.Url != nil {
		loc["confluenceLocation"] = map[string]any{"url": *l.ConfluenceLocation.Url}
	}
	if l.SalesforceLocation != nil && l.SalesforceLocation.Url != nil {
		loc["salesforceLocation"] = map[string]any{"url": *l.SalesforceLocation.Url}
	}
	if l.SharePointLocation != nil && l.SharePointLocation.Url != nil {
		loc["sharePointLocation"] = map[string]any{"url": *l.SharePointLocation.Url}
	}
	if l.WebLocation != nil && l.WebLocation.Url != nil {
		loc["webLocation"] = map[string]any{"url": *l.WebLocation.Url}
	}
	if l.KendraDocumentLocation != nil && l.KendraDocumentLocation.Uri != nil {
		loc["kendraDocumentLocation"] = map[string]any{"uri": *l.KendraDocumentLocation.Uri}
	}
	if l.CustomDocumentLocation != nil && l.CustomDocumentLocation.Id != nil {
		loc["customDocumentLocation"] = map[string]any{"id": *l.CustomDocumentLocation.Id}
	}
	if l.SqlLocation != nil && l.SqlLocation.Query != nil {
		loc["sqlLocation"] = map[string]any{"query": *l.SqlLocation.Query}
	}
	return loc
}

func buildMetadata(md map[string]bardoc.Interface) map[string]any {
	if len(md) == 0 {
		return nil
	}
	out := make(map[string]any, len(md))
	for k, v := range md {
		if v == nil {
			out[k] = nil
			continue
		}
		var decoded any
		if err := v.UnmarshalSmithyDocument(&decoded); err != nil {
			out[k] = fmt.Sprintf("%v", v)
			continue
		}
		out[k] = decoded
	}
	return out
}

func authFromConfig(config map[string]any) awsAuth {
	return awsAuth{
		region:          configString(config, "region", "us-east-1"),
		accessKeyID:     configString(config, "aws_access_key_id", ""),
		secretAccessKey: configString(config, "aws_secret_access_key", ""),
		roleARN:         configString(config, "role_arn", ""),
		externalID:      configString(config, "external_id", ""),
		sessionName:     configString(config, "session_name", ""),
	}
}

func authFromToolContext(tc *sdk.ToolContext) awsAuth {
	return awsAuth{
		region:          tc.GetConfigString("region", "us-east-1"),
		accessKeyID:     tc.GetConfigString("aws_access_key_id", ""),
		secretAccessKey: tc.GetConfigString("aws_secret_access_key", ""),
		roleARN:         tc.GetConfigString("role_arn", ""),
		externalID:      tc.GetConfigString("external_id", ""),
		sessionName:     tc.GetConfigString("session_name", ""),
	}
}

func configFromToolContext(tc *sdk.ToolContext) map[string]any {
	config := map[string]any{}
	for _, key := range []string{"kb_id", "region", "max_results", "tool_name", "tool_description",
		"aws_access_key_id", "aws_secret_access_key", "role_arn", "external_id", "session_name"} {
		if v, ok := tc.GetConfig(key); ok {
			config[key] = v
		}
	}
	return config
}

func configString(config map[string]any, key, defaultVal string) string {
	v, ok := config[key]
	if !ok {
		return defaultVal
	}
	s, ok := v.(string)
	if !ok {
		return defaultVal
	}
	return s
}

func configInt(config map[string]any, key string, defaultVal int) int {
	v, ok := config[key]
	if !ok {
		return defaultVal
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return defaultVal
}
