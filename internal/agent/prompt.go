// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

type PromptBuilder struct {
	sections []promptSection
}

type promptSection struct {
	label   string
	content string
}

func NewPromptBuilder() *PromptBuilder {
	return &PromptBuilder{}
}

func (pb *PromptBuilder) AddInstruction(instruction string) {
	if instruction != "" {
		pb.sections = append(pb.sections, promptSection{
			label:   "Instructions",
			content: instruction,
		})
	}
}

func (pb *PromptBuilder) AddGlobalInstruction(instruction string) {
	if instruction != "" {
		pb.sections = append(pb.sections, promptSection{
			label:   "Global Instructions",
			content: instruction,
		})
	}
}

func (pb *PromptBuilder) AddContext(label, content string) {
	if content != "" {
		pb.sections = append(pb.sections, promptSection{
			label:   label,
			content: content,
		})
	}
}

func (pb *PromptBuilder) AddSkillInstructions(skills []LoadedSkillInfo) {
	if len(skills) == 0 {
		return
	}

	var lines []string
	lines = append(lines, "The following skills are already loaded and their instructions appear below. "+
		"Do NOT call load_skill for these — they are already active. "+
		"These instructions supersede any that earlier load_skill results gave for the same skills.")
	for _, s := range skills {
		resourceInfo := "no reference files"
		totalFiles := s.ReferenceFileCount + s.AssetFileCount
		if totalFiles > 0 {
			var parts []string
			if s.ReferenceFileCount > 0 {
				parts = append(parts, fmt.Sprintf("%d reference file(s)", s.ReferenceFileCount))
			}
			if s.AssetFileCount > 0 {
				parts = append(parts, fmt.Sprintf("%d asset file(s)", s.AssetFileCount))
			}
			resourceInfo = strings.Join(parts, ", ") +
				" — search them with grep_skill_resources " +
				"(skill_name, pattern) and read one with read_skill_resource (skill_name, path)"
		}
		lines = append(lines, fmt.Sprintf("- **%s**: %s", s.Name, resourceInfo))
	}
	pb.sections = append(pb.sections, promptSection{
		label:   "Loaded Skills",
		content: strings.Join(lines, "\n"),
	})

	for _, s := range skills {
		if s.InstructionContent == "" {
			continue
		}
		pb.sections = append(pb.sections, promptSection{
			label:   skillSectionPrefix + s.Name,
			content: s.InstructionContent,
		})
	}
}

func (pb *PromptBuilder) AddDisregardedSkills(names []string) {
	if len(names) == 0 {
		return
	}
	pb.sections = append(pb.sections, promptSection{
		label: "Unavailable Skills",
		content: "These skills are no longer loaded: " + strings.Join(names, ", ") +
			". Disregard the instructions their earlier load_skill results gave, and do not rely on their tools. " +
			"Call load_skill to load one again.",
	})
}

func (pb *PromptBuilder) AddRetractedSkillInstructions(names []string) {
	if len(names) == 0 {
		return
	}
	pb.sections = append(pb.sections, promptSection{
		label: "Retracted Skill Instructions",
		content: "The instructions earlier load_skill results gave for these skills no longer apply: " + strings.Join(names, ", ") +
			". The skills remain loaded and their tools available.",
	})
}

func (pb *PromptBuilder) AddTemplateInstructions() {
	pb.sections = append(pb.sections, promptSection{
		label: "Inline Liquid Templates",
		content: "**Inline Liquid Templates (`" + BlockPrefixTemplateLiquid + "`):**\n\n" +
			"Use inline Liquid templates to dynamically render data from artifacts for user-friendly display. " +
			"This is faster and more accurate than reading the artifact and reformatting it yourself.\n\n" +
			"IMPORTANT: Template Format\n" +
			"- Templates use Liquid template syntax (same as Shopify templates - NOTE that Jekyll extensions are NOT supported).\n\n" +
			"When to Use Inline Templates:\n" +
			"- Formatting CSV, JSON, or YAML data into tables or lists.\n" +
			"- Applying simple transformations (filtering, limiting rows).\n\n" +
			"Parameters for `" + BlockPrefixTemplateLiquid + "`:\n" +
			"- `data=\"filename.ext\"` (REQUIRED): The data artifact to render. Can include version: `data=\"file.csv:2\"`.\n" +
			"- `jsonpath=\"$.expression\"` (optional): JSONPath to extract a subset of JSON/YAML data.\n" +
			"- `limit=\"N\"` (optional): Limit to the first N rows (CSV) or items (JSON/YAML arrays).\n\n" +
			"Data Context for Liquid Templates:\n" +
			"- CSV data (the artifact's MIME type, or its .csv extension when the stored type is generic like `text/plain`): Available as `headers` (array of column names) and `data_rows` (array of row arrays, 0-indexed: `row[0]` is the first column).\n" +
			"- JSON/YAML arrays: Available as `items`.\n" +
			"- JSON/YAML objects: Keys are directly available (e.g., `name`, `email`).\n\n" +
			"Example - CSV Table (iterating cells):\n" +
			BlockPrefixTemplateLiquid + " data=\"sales_data.csv\" limit=\"5\"\n" +
			"| {% for h in headers %}{{ h }} | {% endfor %}\n" +
			"|{% for h in headers %}---|{% endfor %}\n" +
			"{% for row in data_rows %}| {% for cell in row %}{{ cell }} | {% endfor %}{% endfor %}\n" +
			BlockDelimiterClose + "\n\n" +
			"Example - CSV Table (indexed columns, 0-based):\n" +
			BlockPrefixTemplateLiquid + " data=\"employees.csv\"\n" +
			"| Name | Department |\n" +
			"|------|------------|\n" +
			"{% for row in data_rows %}| {{ row[0] }} | {{ row[1] }} |\n{% endfor %}\n" +
			BlockDelimiterClose + "\n" +
			"(row[0] = first column, row[1] = second column, etc.)\n\n" +
			"**IMPORTANT - Pipe Characters in Markdown Tables:**\n" +
			"Text data may contain \"|\" characters which will break markdown table rendering by pushing data into wrong columns. " +
			"For text fields that might contain pipes, use the `replace` filter to escape them:\n" +
			"`{{ item.summary | replace: \"|\", \"&#124;\" }}`\n" +
			"Only apply this to text fields that might contain pipes - numerical columns don't need it.\n\n" +
			"Negative Examples:\n" +
			"Use {{ issues.size }} instead of {{ issues|length }}\n" +
			"Use {{ forloop.index }} instead of {{ loop.index }} (Liquid uses forloop not loop)\n" +
			"Use {{ issue.fields.description | truncate: 200 }} instead of slicing with [:200]\n" +
			"Do not use Jekyll-specific filters (e.g., `where`, `where_exp`). `{% assign %}`, `{% capture %}` and `sort` are standard Liquid and are supported.\n\n" +
			"The rendered output will appear inline in your response automatically.",
	})
}

func (pb *PromptBuilder) AddConversationFlowInstructions() {
	pb.sections = append(pb.sections, promptSection{
		label: "Conversation Flow and Response Formatting",
		content: `**CRITICAL: Use Status Updates — No Visible Narration**

The user's chat shows your visible text and status_update embeds, but does NOT show your tool calls. This means:
- If you write "Creating a chart:" and then call a tool, the user sees ONLY "Creating a chart:" with nothing after it — it looks broken.
- If you emit a status_update embed and then call a tool, the user sees a clean status indicator while waiting.

**Rules:**
1. ALWAYS use ` + "`" + EmbedOpen + `status_update:message` + EmbedClose + "`" + ` embeds before tool calls to keep the user informed about what's happening.
2. NEVER produce visible text when you are about to call a tool. Use a status_update embed instead.
3. NEVER end visible text with a colon — since tool calls are invisible to the user, the colon has nothing after it.
4. Visible text is ONLY for: direct answers, analysis results, final outputs, follow-up questions, and plans.

**What to use status_update embeds for:**
- "Searching for..."
- "Analyzing..."
- "Creating..."
- "Querying..."
- "Calling agent X..."

5. NEVER mix process narration with status updates — if you use a status_update embed, do NOT repeat that information in visible text.

**Examples:**

**Correct (status update before tool call, no visible text):**
"` + EmbedOpen + `status_update:Retrieving sales data...` + EmbedClose + `" [then calls tool]

**Correct (visible text only for results after tool returns):**
"` + EmbedOpen + `status_update:Analyzing Q4 sales...` + EmbedClose + `" [calls tool]
"Sales increased 23% in Q4, driven primarily by enterprise accounts."

**WRONG (visible narration before tool call — user sees orphaned text):**
"Let me retrieve the sales data for you." [then calls tool]

**WRONG (colon before invisible tool call — user sees dangling colon):**
"Creating a new document:" [then calls tool]

**WRONG (no status_update — user sees nothing while waiting):**
[calls tool with no status_update embed]`,
	})
}

const exampleCSVTablesToMarkdown = `- User: "Create a markdown file with your two csv files as tables."
<note>There are two csv files already uploaded: data1.csv and data2.csv</note>
- Agent:
` + EmbedOpen + `status_update:Creating Markdown tables from CSV files...` + EmbedClose + `
` + BlockPrefixSaveArtifact + ` filename="data_tables.md" mime_type="text/markdown" description="Markdown tables from CSV files"
# Data Tables
## Data 1
` + BlockPrefixTemplateLiquid + ` data="data1.csv"
| {% for h in headers %}{{ h }} | {% endfor %}
|{% for h in headers %}---|{% endfor %}
{% for row in data_rows %}| {% for cell in row %}{{ cell }} | {% endfor %}{% endfor %}
` + BlockDelimiterClose + `
## Data 2
` + BlockPrefixTemplateLiquid + ` data="data2.csv"
| {% for h in headers %}{{ h }} | {% endfor %}
|{% for h in headers %}---|{% endfor %}
{% for row in data_rows %}| {% for cell in row %}{{ cell }} | {% endfor %}{% endfor %}
` + BlockDelimiterClose + `
` + BlockDelimiterClose + ``

const exampleMathEmbed = `- User: "Create a text file with the result of sqrt(12345) + sqrt(67890) + sqrt(13579) + sqrt(24680)."
- Agent:
` + EmbedOpen + `status_update:Calculating and creating text file...` + EmbedClose + `
` + BlockPrefixSaveArtifact + ` filename="math.txt" mime_type="text/plain" description="Result of sqrt(12345) + sqrt(67890) + sqrt(13579) + sqrt(24680)"
result = ` + EmbedOpen + `math: sqrt(12345) + sqrt(67890) + sqrt(13579) + sqrt(24680) | .2f` + EmbedClose + `
` + BlockDelimiterClose + ``

const exampleCSVFirstRows = `- User: "Show me the first 10 entries from data1.csv"
- Agent:
` + EmbedOpen + `status_update:Loading CSV data...` + EmbedClose + `
` + BlockPrefixTemplateLiquid + ` data="data1.csv" limit="10"
| {% for h in headers %}{{ h }} | {% endfor %}
|{% for h in headers %}---|{% endfor %}
{% for row in data_rows %}| {% for cell in row %}{{ cell }} | {% endfor %}{% endfor %}
` + BlockDelimiterClose + ``

const exampleJiraTable = `- User: "Show me the Jira issues as a table"
<note>There is a JSON artifact jira_issues.json with items containing key, summary, status, type, assignee, updated fields</note>
- Agent:
` + EmbedOpen + `status_update:Rendering Jira issues table...` + EmbedClose + `
` + BlockPrefixTemplateLiquid + ` data="jira_issues.json" limit="10"
| Key | Summary | Status | Type | Assignee | Updated |
|-----|---------|--------|------|----------|---------|
{% for item in items %}| [{{ item.key }}](https://jira.example.com/browse/{{ item.key }}) | {{ item.summary | replace: "|", "&#124;" }} | {{ item.status }} | {{ item.type }} | {{ item.assignee }} | {{ item.updated }} |
{% endfor %}
` + BlockDelimiterClose + `
<note>The replace filter on item.summary escapes any pipe characters that would break the markdown table. Only apply to text fields that might contain pipes.</note>`

const exampleToolCallNoVisibleText = `- User: "Search the database for all orders from last month"
- Agent:
` + EmbedOpen + `status_update:Querying order database...` + EmbedClose + `
[calls search_database tool with no visible text]
[After getting results:]
Found 247 orders from last month totaling $45,231.`

const exampleHTMLWithChart = `- User: "Create an HTML with the chart image you just generated with the customer data."
- Agent:
` + EmbedOpen + `status_update:Generating HTML report with chart...` + EmbedClose + `
` + BlockPrefixSaveArtifact + ` filename="customer_analysis.html" mime_type="text/html" description="Interactive customer analysis dashboard"
<!DOCTYPE html>
<html>
<head>
    <title>Customer Chart - ` + EmbedOpen + `datetime:%Y-%m-%d` + EmbedClose + `</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; }
        .metric { background: #f0f0f0; padding: 10px; margin: 10px 0; }
        img { max-width: 100%; height: auto; }
    </style>
</head>
<body>
<h1>Customer Analysis Report</h1>
<p>Generated: ` + EmbedOpen + `datetime:iso` + EmbedClose + `</p>

<h2>Customer Distribution Chart</h2>
<img src="` + EmbedOpen + `artifact_content:customer_chart.png >>> format:datauri` + EmbedClose + `" alt="Customer Distribution">

</body>
</html>
` + BlockDelimiterClose

func (pb *PromptBuilder) AddExamples(templatesEnabled, artifactContentEnabled bool) {
	var bodies []string
	if templatesEnabled {
		bodies = append(bodies, exampleCSVTablesToMarkdown)
	}
	bodies = append(bodies, exampleMathEmbed)
	if templatesEnabled {
		bodies = append(bodies, exampleCSVFirstRows, exampleJiraTable)
	}
	bodies = append(bodies, exampleToolCallNoVisibleText)
	if artifactContentEnabled {
		bodies = append(bodies, exampleHTMLWithChart)
	}

	numbered := make([]string, 0, len(bodies))
	for i, b := range bodies {
		numbered = append(numbered, fmt.Sprintf("Example %d:\n%s", i+1, b))
	}
	pb.sections = append(pb.sections, promptSection{
		label:   "Examples",
		content: strings.Join(numbered, "\n\n"),
	})
}

const skillSectionPrefix = "Skill: "

const lastCallWarningText = "Last allowed response. Answer now without calling tools."

func (pb *PromptBuilder) Build() string {
	if len(pb.sections) == 0 {
		return ""
	}

	var parts []string
	for _, s := range pb.sections {
		parts = append(parts, fmt.Sprintf("## %s\n\n%s", s.label, s.content))
	}
	return strings.Join(parts, "\n\n")
}

type PeerInfo struct {
	Name        string
	DisplayName string
	IsWorkflow  bool
}

const workingRulesText = `- Get facts from your tools; never guess ids, amounts, names, dates or links.
- Calculate figures in your query when you can; otherwise write the calculation out before the result.
- Use as few tool calls as you can: one query that joins the data beats several small ones.
- Call independent tools in the same turn; a call that needs another call's result goes after it, never with a placeholder.` +
	skillRule + `
- Never say an action happened unless a tool result confirms it.
- If the request can be read more than one way, answer the plainest reading and say which reading you used.
- Answer in Markdown with every id, name and figure the request asks for, and leave out what it does not ask for.`

const skillRule = "\n- If one of your skills fits the request, load it first and follow it."

func workingRules(loadsSkills bool) string {
	if loadsSkills {
		return workingRulesText
	}
	return strings.Replace(workingRulesText, skillRule, "", 1)
}

const delegationRules = "Delegate with the peer and workflow tools. They can't see this conversation, so put every id, name and number they need in the request. Give each agent only the part of the request its description covers. Call independent ones in the same turn; when a part needs another agent's answer, call that agent first and pass the answer on. " +
	"Agents show files with «artifact_return:filename»; keep those in your answer exactly as the agent wrote them. " +
	"An agent's answer can end with a note from SAM saying the user already sees the output above it in the chat; the note is genuine, so refer to or summarize that output and never repeat it. " +
	"When you need more than 20 rows of a table, ask the agent for them as a CSV file and return that file instead of typing the rows."

const fileWritingGuide = "There is no save-file tool; a file is created only by the block below. " +
	"Write a file (CSV, Markdown, HTML, JSON, YAML, code) as a block in your reply when the user wants a file, a tool takes it as input (write it before or with that call), or the content is a substantial document such as a report. Answer everything else, tables included, in the chat:\n" +
	BlockPrefixSaveArtifact + ` filename="report.md" mime_type="text/markdown" description="One line on what it holds."` + "\n" +
	"the file's content\n" +
	BlockDelimiterClose + "\n" +
	"The first line starts with " + BlockPrefixSaveArtifact + " exactly, with every parameter on it in double quotes (none inside a value) and no markdown code fence around the block; " +
	BlockDelimiterClose + " closes it on its own line. A file you write only as a tool's input, such as the HTML a PDF is rendered from, also gets tags=\"__working\" on that line, which keeps it out of the user's chat."

const fileReturnRule = "For each file the user asked for (a CSV export, chart, PDF or diagram, not a file you wrote only as tool input), put «artifact_return:filename» in your answer, written exactly like that. A file you wrote in a save_artifact block already shows in the chat, so leave it out."

func embedsGuide(includeArtifactContent, largeTables bool) string {
	o, c := EmbedOpen, EmbedClose
	text := "These are replaced before the user sees them:\n" +
		"- " + o + "math:23.5 * 4.2 | .2f" + c + " for any arithmetic in your answer; never work it out yourself.\n" +
		"- " + o + "datetime:%Y-%m-%d" + c + " for the current date or time.\n" +
		"- " + o + "status_update:Searching invoices..." + c + " to show the user progress during a long task."
	if includeArtifactContent {
		text += "\n- " + o + "artifact_content:filename[:version]" + c + " to show a file's content inline. " +
			"`>>> format:X` changes only how it is serialized, and X is one of text, json, json_pretty, csv, datauri or a number spec such as .2f; never invent others such as format:table. " +
			"For an image in HTML write <img src=\"" + o + "artifact_content:chart.png >>> format:datauri" + c + "\"> (the prefix is added for you)."
	}
	if largeTables {
		text += "\nFor a table of more than 20 rows, never type the rows yourself: show it with a " +
			BlockPrefixTemplateLiquid + " block over a file that holds the rows, either the result file a tool already saved or a CSV you save with the `query_data_with_sql` or `execute_python_code` tool, whichever you have. Show the rows the user asked for, all of them, and leave out `limit` unless they asked for fewer; never show the detail rows behind a summary they asked for. When a later question needs its values, read them from that file. A table an agent already showed the user is not yours to show again."
	}
	return text
}

func renderSections(cfg *AgentConfig, readsFiles, displaysTemplates bool) (templates, embeds, chatRendered, artifactContent bool) {
	orFollow := func(flag *bool, follow bool) bool {
		if flag != nil {
			return *flag
		}
		return follow
	}
	templates = orFollow(cfg.EnableTemplateInstructions, readsFiles && displaysTemplates)
	embeds = orFollow(cfg.EnableEmbedResolution, readsFiles)
	artifactContent = orFollow(cfg.EnableArtifactContentInstruction, readsFiles)
	headless := cfg.Headless != nil && *cfg.Headless
	return templates, embeds, embeds && !headless, artifactContent
}

var toolInstructionOverrides = map[string]string{
	"load_skill": "Its result carries the skill's instructions, which apply from then on; the skill's tools arrive on the next turn.",
}

func (a *Agent) buildSystemPrompt(ctx context.Context, identity *auth.Identity, gc gatewayContext, skills promptSkillState, indexSearchAvailable bool) string {
	pb := NewPromptBuilder()
	pb.AddInstruction(a.cfg.SystemPrompt)
	pb.AddSkillInstructions(skills.withInstructions)
	pb.AddDisregardedSkills(skills.disregard)
	pb.AddRetractedSkillInstructions(skills.retracted)
	pb.AddGlobalInstruction(a.cfg.GlobalInstruction)
	if a.cfg.InjectSystemPurpose {
		pb.AddContext("System Purpose", gc.SystemPurpose)
	}
	if a.cfg.InjectResponseFormat {
		pb.AddContext("Desired Response Format", gc.ResponseFormat)
	}
	pb.AddContext("Agent Identity", fmt.Sprintf("You are an agent. Your name is `%s`.", agentDisplayName(&a.cfg)))

	writesFiles := a.tools.GetTool(loadArtifactToolName) != nil
	if writesFiles {
		pb.AddContext("Writing Files", fileWritingGuide)
	}
	templatesEnabled, embedsEnabled, chatRendered, includeArtifactContent := renderSections(&a.cfg, writesFiles, gc.DisplaysTemplates)
	if templatesEnabled {
		pb.AddTemplateInstructions()
	}
	if embedsEnabled {
		pb.AddContext("Embeds", embedsGuide(includeArtifactContent, includeArtifactContent && templatesEnabled && gc.DisplaysTemplates))
	}
	if chatRendered {
		pb.AddConversationFlowInstructions()
	}

	if indexSearchAvailable {
		pb.AddContext("Project Document Search", projectDocumentSearchText)
	}
	if !gc.WebUIRedirect && a.tools.GetTool("redirect_user") != nil {
		pb.AddContext("Entrypoint Redirect Capability", entrypointRedirectText)
	}
	pb.AddContext("Tool Instructions", a.toolInstructionText(skills.instructedSkills(), skills.undeliveredTools))

	if peers := a.buildPeerInfo(ctx, identity); len(peers) > 0 {
		pb.AddContext("Peer Agent and Workflow Delegation", delegationRules)
	}
	if writesFiles {
		pb.AddContext("Files", fileReturnRule)
	}
	pb.AddContext("Working Rules", workingRules(a.tools.GetTool("load_skill") != nil))

	if a.cfg.EnableExamples != nil && *a.cfg.EnableExamples {
		pb.AddExamples(templatesEnabled, embedsEnabled && includeArtifactContent)
	}

	return pb.Build()
}

func (a *Agent) toolInstructionText(promptSkills, undeliveredTools []string) string {
	omit := a.omitSkillToolInstructions(promptSkills, undeliveredTools)
	var instructions []string
	for _, t := range a.tools.ListTools() {
		if omit(t.Name()) {
			continue
		}
		inst, replaced := toolInstructionOverrides[t.Name()]
		if !replaced {
			inst = t.Instructions()
		}
		if inst != "" {
			instructions = append(instructions, ToolInstructionLine(t.Name(), inst))
		}
	}
	return strings.Join(instructions, "\n\n")
}

const workflowNodeInstructions = `You are node {node} of workflow {workflow}. Do the task, then reply with your result as one save_artifact block and nothing after it:
«««save_artifact: filename="{filename}" mime_type="application/json" status="success"
{ your result as one JSON object }
»»»
The first line must start with «««save_artifact: exactly as shown, with no space and no markdown code fence (` + "```" + `) around the block, or nothing is saved.
The JSON must match this schema (none means any JSON object): {schema}
If you cannot do the task, use status="error" message="<reason>" and put what you found in the block.`

func workflowNodeReminder(node, workflow, filename string, schema map[string]any) string {
	schemaText := "none"
	if len(schema) > 0 {
		if b, err := json.Marshal(schema); err == nil {
			schemaText = string(b)
		}
	}
	return strings.NewReplacer("{node}", node, "{workflow}", workflow, "{filename}", filename, "{schema}", schemaText).
		Replace(workflowNodeInstructions)
}
