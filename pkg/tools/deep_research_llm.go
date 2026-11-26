// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

type llmCallFunc func(ctx context.Context, systemPrompt, userPrompt string, temperature float64) (string, error)

type ReflectionResult struct {
	QualityScore     float64  `json:"quality_score"`
	Gaps             []string `json:"gaps"`
	ShouldContinue   bool     `json:"should_continue"`
	SuggestedQueries []string `json:"suggested_queries"`
	Reasoning        string   `json:"reasoning"`
}

func generateInitialQueries(ctx context.Context, llmCall llmCallFunc, question string) ([]string, error) {
	prompt := fmt.Sprintf(`You are a research query specialist. Generate 3-5 effective search queries to comprehensively research this question:

Research Question: %s

Generate queries that:
1. Cover different aspects of the topic
2. Use varied terminology and perspectives
3. Range from broad to specific
4. Are optimized for search engines

Respond in JSON format:
{
  "queries": ["query1", "query2", "query3", "query4", "query5"]
}`, question)

	resp, err := llmCall(ctx, "", prompt, 0.7)
	if err != nil {
		return []string{question}, nil
	}

	var result struct {
		Queries []string `json:"queries"`
	}
	if err := parseJSONFromLLMResponse(resp, &result, "queries"); err != nil || len(result.Queries) == 0 {
		return []string{question}, nil
	}
	return result.Queries, nil
}

func generateResearchTitle(ctx context.Context, llmCall llmCallFunc, question string) string {
	prompt := fmt.Sprintf(`Generate a concise, human-readable title for this research topic.

Research Question: %s

Requirements:
1. The title should be 5-10 words maximum
2. It should capture the essence of the research topic
3. It should be suitable for display as a heading
4. Do NOT include quotes around the title
5. Do NOT include "Research:" or similar prefixes

Respond with ONLY the title, nothing else.`, question)

	resp, err := llmCall(ctx, "", prompt, 0.3)
	if err != nil || strings.TrimSpace(resp) == "" {
		if len(question) > 60 {
			return question[:60] + "..."
		}
		return question
	}

	title := strings.TrimSpace(resp)
	title = strings.Trim(title, `"'`)
	if len(title) > 100 {
		title = title[:100]
	}
	return title
}

func selectSourcesToFetch(ctx context.Context, llmCall llmCallFunc, question string, findings []researchFinding, maxToFetch int) ([]int, error) {
	if len(findings) == 0 {
		return nil, nil
	}

	var b strings.Builder
	for i, f := range findings {
		snippet := f.Content
		if len(snippet) > 150 {
			snippet = snippet[:150] + "..."
		}
		fmt.Fprintf(&b, "%d. %s\n   URL: %s\n   Snippet: %s\n   Relevance: %.2f\n\n", i+1, f.Title, f.URL, snippet, f.RelevanceScore)
	}

	prompt := fmt.Sprintf(`You are a research quality analyst. Select the %d BEST sources to fetch full content from for this research question:

Research Question: %s

Available Sources:
%s

Select the %d sources that are most likely to provide:
1. Authoritative, credible information (e.g., .edu, .gov, established organizations)
2. Comprehensive coverage of the topic
3. Unique perspectives or data
4. Academic or expert analysis

You MUST respond with ONLY valid JSON in this exact format:
{
  "selected_sources": [1, 3, 5],
  "reasoning": "Brief explanation"
}

Do not include any other text, markdown formatting, or explanations outside the JSON.`, maxToFetch, question, b.String(), maxToFetch)

	resp, err := llmCall(ctx, "", prompt, 0.3)
	if err != nil {
		return nil, err
	}

	var result struct {
		SelectedSources []int  `json:"selected_sources"`
		Reasoning       string `json:"reasoning"`
	}
	if err := parseJSONFromLLMResponse(resp, &result, "selected_sources"); err != nil {
		return nil, err
	}

	indices := make([]int, 0, len(result.SelectedSources))
	for _, idx := range result.SelectedSources {
		zeroIdx := idx - 1
		if zeroIdx >= 0 && zeroIdx < len(findings) {
			indices = append(indices, zeroIdx)
		}
	}
	return indices, nil
}

func reflectOnFindings(ctx context.Context, llmCall llmCallFunc, question, findingsSummary string, iteration, maxIterations int) (*ReflectionResult, error) {
	prompt := fmt.Sprintf(`You are a research quality analyst. Analyze the current research findings and provide guidance for the next research iteration.

Research Question: %s

Current Iteration: %d

Findings Summary:
%s

Please analyze these findings and provide:

1. **Quality Score** (0.0 to 1.0): How complete and comprehensive is the current research?
   - 0.0-0.3: Very incomplete, major gaps
   - 0.4-0.6: Partial coverage, significant gaps remain
   - 0.7-0.8: Good coverage, minor gaps
   - 0.9-1.0: Comprehensive, excellent coverage

2. **Knowledge Gaps**: What important aspects are missing or under-covered?

3. **Should Continue**: Should we conduct another research iteration? (yes/no)
   - Consider: quality score, iteration number, diminishing returns
   - Maximum iterations allowed: %d

4. **Suggested Queries**: If continuing, what 3-5 specific search queries would fill the gaps?

Respond in JSON format:
{
  "quality_score": 0.0,
  "gaps": ["gap1", "gap2"],
  "should_continue": true,
  "suggested_queries": ["query1", "query2"],
  "reasoning": "Brief explanation of your assessment"
}`, question, iteration, findingsSummary, maxIterations)

	resp, err := llmCall(ctx, "", prompt, 0.3)
	if err != nil {
		return &ReflectionResult{
			QualityScore:   0.5,
			ShouldContinue: iteration < 3,
			Reasoning:      "LLM reflection failed, defaulting",
		}, nil
	}

	var result ReflectionResult
	if err := parseJSONFromLLMResponse(resp, &result, "quality_score"); err != nil {
		return &ReflectionResult{
			QualityScore:   0.5,
			ShouldContinue: iteration < 3,
			Reasoning:      "Failed to parse reflection response",
		}, nil
	}
	return &result, nil
}

func generateResearchReport(ctx context.Context, llmCall llmCallFunc, question, title, findingsText string, numFindings int) (string, error) {
	prompt := fmt.Sprintf(`You are an expert research analyst. Your task is to SYNTHESIZE information from multiple sources into an original, comprehensive research report.

Research Question: %s

You have access to %d sources below. Your job is to READ ALL OF THEM, extract key information, and create a well-written report.

Source Materials:
%s

CRITICAL INSTRUCTIONS:

⚠️ DO NOT COPY: You must NOT copy text directly from any single source. You must SYNTHESIZE information from MULTIPLE sources.

⚠️ ORIGINAL WRITING: Write in your own words, combining insights from different sources.

⚠️ DO NOT INCLUDE WORD COUNTS: Do NOT include word count targets (like "300-500 words") in your section headings or anywhere in the output. These are internal guidelines for you only.

REPORT STRUCTURE GUIDELINES (aim for 3000-5000 words total, but DO NOT mention word counts in output):

Write the following sections WITHOUT including word count targets in headings:

## Executive Summary
Synthesize the MOST IMPORTANT insights from ALL sources. Highlight key findings that answer the research question. Provide context for why this topic matters. DO NOT copy from any single source.

## Introduction
Explain the research question and its significance. Provide historical or contextual background. Outline what the report will cover. Draw context from multiple sources [[cite:researchX]].

## Main Analysis
Organize into 5-8 thematic sections with descriptive headings (###). For EACH section:
- Create a descriptive heading like "### Historical Development" or "### Economic Impact" (NO word counts)
- Draw information from multiple sources
- Start each paragraph with a topic sentence
- Support claims with citations from different sources.[[cite:researchX]][[cite:researchY]]
- Explain implications and connections
- Compare and contrast different perspectives
- NEVER copy paragraphs from a single source

## Comparative Analysis
Compare different perspectives across sources. Identify agreements and contradictions. Analyze why sources might differ. Synthesize a balanced view. Cite multiple sources for each point.

## Implications
Discuss practical implications. Identify applications or consequences. Suggest areas needing further research. Draw from multiple sources.

## Conclusion
Synthesize the key takeaways from ALL sources. Provide final analytical insights. Suggest future directions.

⚠️ DO NOT CREATE A REFERENCES SECTION: The system will automatically append a properly formatted References section with all cited sources. Your report should end with the Conclusion section.

CITATION RULES:
- Use [[cite:researchN]] format where N is the citation number from sources above
- Place citations AFTER the period at the end of sentences (e.g., "This is a fact.[[cite:research0]]")
- Use multiple citations when multiple sources support a point: .[[cite:research0]][[cite:research2]]
- Cite sources even when paraphrasing

QUALITY CHECKS:
✓ Have I synthesized from MULTIPLE sources (not just one)?
✓ Have I written in my OWN words (not copied)?
✓ Have I cited ALL factual claims?
✓ Have I organized information thematically (not source-by-source)?
✓ Have I avoided including word count targets in my output?

Write your research report now. Format in Markdown. Remember: NO word counts in section headings or anywhere in the output.`, question, numFindings, findingsText)

	report, err := llmCall(ctx, "", prompt, 1.0)
	if err != nil {
		return "", fmt.Errorf("report generation failed: %w", err)
	}
	return report, nil
}

func prepareFindingsSummary(findings []researchFinding) string {
	if len(findings) == 0 {
		return "No findings yet."
	}

	byType := make(map[string][]researchFinding)
	for _, f := range findings {
		byType[f.SourceType] = append(byType[f.SourceType], f)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Total Sources: %d\n", len(findings))

	typeNames := make([]string, 0, len(byType))
	for t := range byType {
		typeNames = append(typeNames, t)
	}
	slices.Sort(typeNames)
	fmt.Fprintf(&b, "Source Types: %s\n\n", strings.Join(typeNames, ", "))

	for _, typeName := range typeNames {
		typeFindings := byType[typeName]
		slices.SortFunc(typeFindings, func(a, b researchFinding) int {
			return cmp.Compare(b.RelevanceScore, a.RelevanceScore)
		})
		fmt.Fprintf(&b, "%s Sources (%d):\n", strings.ToUpper(typeName), len(typeFindings))

		limit := 5
		if len(typeFindings) < limit {
			limit = len(typeFindings)
		}
		for i, f := range typeFindings[:limit] {
			title := f.Title
			if len(title) > 80 {
				title = title[:80] + "..."
			}
			content := f.Content
			if len(content) > 150 {
				content = content[:150] + "..."
			}
			fmt.Fprintf(&b, "  %d. %s\n", i+1, title)
			fmt.Fprintf(&b, "     %s\n", content)
			fmt.Fprintf(&b, "     Relevance: %.2f\n", f.RelevanceScore)
		}
		b.WriteString("\n")
	}

	return b.String()
}

const (
	reportContentMaxChars  = 5000
	reportSourcesPerGroup  = 15
	reportPayloadMaxBytes  = 60000
	reportTruncationMarker = "\n[Content continues but truncated for length...]"
)

var (
	markupTagPattern  = regexp.MustCompile(`<[^>]*>`)
	looksLikeMarkupRe = regexp.MustCompile(`(?i)</?(a|b|i|p|u|em|strong|div|span|br|hr|h[1-6]|ul|ol|li|table|thead|tbody|tr|td|th|img|html|head|body|title|meta|link|script|style|svg|noscript|iframe|form|input|button|pre|code|center|font)(\s[^>]*)?/?>|<!--|<!doctype|<\?xml`)
)

const noPayloadCap = math.MaxInt

func plainText(s string) string {
	if !looksLikeMarkupRe.MatchString(s) {
		return s
	}
	if text, err := pageHTMLToText(s); err == nil && text != "" {
		return text
	}
	return strings.TrimSpace(markupTagPattern.ReplaceAllString(s, " "))
}

func prepareFindingsForReport(findings []researchFinding) (string, []researchFinding) {
	return buildFindingsPayload(findings, noPayloadCap)
}

func prepareFindingsWithinBudget(findings []researchFinding, maxBytes int) (string, []researchFinding) {
	return buildFindingsPayload(rankedByRelevance(findings), maxBytes)
}

func buildFindingsPayload(findings []researchFinding, maxBytes int) (string, []researchFinding) {
	var b strings.Builder
	b.WriteString("# Research Findings\n\n")

	var fetched, snippets, included []researchFinding
	for _, f := range findings {
		if f.Fetched {
			fetched = append(fetched, f)
		} else {
			snippets = append(snippets, f)
		}
	}

	writeGroup := func(group []researchFinding, render func(researchFinding) string) {
		for _, f := range group[:min(len(group), reportSourcesPerGroup)] {
			entry := render(f)
			if maxBytes != noPayloadCap && b.Len()+len(entry) > maxBytes {
				continue
			}
			b.WriteString(entry)
			included = append(included, f)
		}
	}

	if len(fetched) > 0 {
		b.WriteString("## Detailed Sources (Full Content Retrieved)\n\n")
		writeGroup(fetched, func(f researchFinding) string {
			content := f.Content
			if runes := []rune(content); len(runes) > reportContentMaxChars {
				content = string(runes[:reportContentMaxChars]) + reportTruncationMarker
			}
			return fmt.Sprintf("### %s\n**Citation ID:** %s\n**URL:** %s\n**Relevance:** %.2f\n\n**Content:**\n%s\n\n---\n\n",
				plainText(f.Title), f.CitationID, f.URL, f.RelevanceScore, content)
		})
	}

	if len(snippets) > 0 {
		b.WriteString("## Additional Sources (Snippets)\n\n")
		writeGroup(snippets, func(f researchFinding) string {
			return fmt.Sprintf("### %s\n**Citation ID:** %s\n**URL:** %s\n**Snippet:** %s\n**Relevance:** %.2f\n\n---\n\n",
				plainText(f.Title), f.CitationID, f.URL, plainText(f.Content), f.RelevanceScore)
		})
	}

	return b.String(), included
}

func generateSourcesSection(findings []researchFinding) string {
	var cited []researchFinding
	for _, f := range findings {
		if f.CitationID != "" {
			cited = append(cited, f)
		}
	}
	if len(cited) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\n---\n\n## References\n\n")

	var webSources, kbSources []researchFinding
	for _, f := range cited {
		if f.SourceType == "kb" {
			kbSources = append(kbSources, f)
		} else {
			webSources = append(webSources, f)
		}
	}

	for _, f := range webSources {
		if f.URL == "" {
			continue
		}
		numStr := strings.TrimPrefix(f.CitationID, "research")
		numStr = strings.TrimPrefix(numStr, "file")
		numStr = strings.TrimPrefix(numStr, "ref")
		num := 0
		if _, err := fmt.Sscanf(numStr, "%d", &num); err == nil {
			num++
		}

		fetchIndicator := " *(search result)*"
		if f.Fetched {
			fetchIndicator = " *(read in full)*"
		}

		fmt.Fprintf(&b, "**[%d]** %s%s  \n%s\n\n", num, f.Title, fetchIndicator, f.URL)
	}

	for _, f := range kbSources {
		if f.CitationID == "" {
			continue
		}
		numStr := strings.TrimPrefix(f.CitationID, "research")
		numStr = strings.TrimPrefix(numStr, "file")
		numStr = strings.TrimPrefix(numStr, "ref")
		num := 0
		if _, err := fmt.Sscanf(numStr, "%d", &num); err == nil {
			num++
		}

		fetchIndicator := " *(search result)*"
		if f.Fetched {
			fetchIndicator = " *(read in full)*"
		}

		fmt.Fprintf(&b, "**[%d]** %s%s\n\n", num, f.Title, fetchIndicator)
	}

	return b.String()
}

func generateMethodologySection(findings []researchFinding) string {
	fetchedCount := 0
	snippetCount := 0
	webCount := 0
	kbCount := 0
	for _, f := range findings {
		if f.Fetched {
			fetchedCount++
		} else {
			snippetCount++
		}
		switch f.SourceType {
		case "kb":
			kbCount++
		default:
			webCount++
		}
	}

	return fmt.Sprintf(`

## Research Methodology

This research analyzed **%d sources** across multiple iterations:

- **%d sources** were read in full detail (cited in References above)
- **%d additional sources** were consulted via search snippets
- Source types: %d web, %d knowledge base

The research process involved:
1. Generating targeted search queries using AI
2. Searching across multiple information sources
3. Selecting the most authoritative and relevant sources
4. Retrieving and analyzing full content from selected sources
5. Synthesizing findings into a comprehensive report
`, len(findings), fetchedCount, snippetCount, webCount, kbCount)
}

var (
	jsonCodeBlockRegex = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")
)

func parseJSONFromLLMResponse(text string, target any, fallbackKey string) error {
	cleanText := strings.TrimSpace(text)

	if strings.HasPrefix(cleanText, "```") {
		if strings.HasPrefix(cleanText, "```json") {
			cleanText = cleanText[7:]
		} else {
			cleanText = cleanText[3:]
		}
		cleanText = strings.TrimSpace(cleanText)
		if strings.HasSuffix(cleanText, "```") {
			cleanText = cleanText[:len(cleanText)-3]
			cleanText = strings.TrimSpace(cleanText)
		}
	}

	if err := json.Unmarshal([]byte(cleanText), target); err == nil {
		return nil
	}

	if matches := jsonCodeBlockRegex.FindStringSubmatch(text); len(matches) > 1 {
		if err := json.Unmarshal([]byte(matches[1]), target); err == nil {
			return nil
		}
	}

	if fallbackKey != "" {
		pattern := regexp.MustCompile(fmt.Sprintf(`(?s)\{[^{}]*%q[^{}]*\}`, fallbackKey))
		if match := pattern.FindString(text); match != "" {
			if err := json.Unmarshal([]byte(match), target); err == nil {
				return nil
			}
		}
	}

	return fmt.Errorf("failed to parse JSON from LLM response (length=%d)", len(text))
}

var (
	nonWordRegex    = regexp.MustCompile(`[^\w\s-]`)
	whitespaceRegex = regexp.MustCompile(`[-\s]+`)
)

func sanitizeToFilename(text string, maxLen int, suffix string) string {
	if text == "" {
		return "unnamed" + suffix
	}

	safe := strings.ToLower(text)
	safe = nonWordRegex.ReplaceAllString(safe, "")
	safe = whitespaceRegex.ReplaceAllString(safe, "_")
	safe = strings.Trim(safe, "_")

	if maxLen > 0 && len(safe) > maxLen {
		safe = safe[:maxLen]
		safe = strings.TrimRight(safe, "_")
	}

	if safe == "" {
		safe = "unnamed"
	}
	return safe + suffix
}
