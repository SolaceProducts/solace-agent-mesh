// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const DefaultTestAgentTTL = 3 * 24 * time.Hour

const systemUserIDSweeper = "system:test-agent-sweeper"

const testAgentUserHashBytes = 8

const testAgentNameInUseMessage = "the test agent name for this session is already in use"

const testHarnessSkillName = "sam-test-harness"

type deploymentControl interface {
	Undeploy(ctx context.Context, agentID, userID string) error
	CreateWithOpts(ctx context.Context, req CreateAgentDeploymentRequest, userID string, extra DeployOptions) (*AgentDeploymentResponse, error)
}

type TestAgentService struct {
	agentRepo     *store.AgentRepo
	workflowRepo  *store.WorkflowRepo
	skillRepo     *store.SkillRepo
	deploymentSvc deploymentControl
	agentSvc      *AgentService
	ttl           time.Duration
	logger        *slog.Logger
}

func NewTestAgentService(
	agentRepo *store.AgentRepo,
	workflowRepo *store.WorkflowRepo,
	skillRepo *store.SkillRepo,
	deploymentSvc *DeploymentService,
	agentSvc *AgentService,
	ttl time.Duration,
	logger *slog.Logger,
) *TestAgentService {
	if ttl <= 0 {
		ttl = DefaultTestAgentTTL
	}
	return &TestAgentService{
		agentRepo:     agentRepo,
		workflowRepo:  workflowRepo,
		skillRepo:     skillRepo,
		deploymentSvc: deploymentSvc,
		agentSvc:      agentSvc,
		ttl:           ttl,
		logger:        logger,
	}
}

const (
	ComponentKindAgent    = "agent"
	ComponentKindWorkflow = "workflow"
	ComponentKindSkill    = "skill"
	ComponentKindA2AAgent = "a2a_agent"
)

func (s *TestAgentService) CreateForSession(ctx context.Context, sessionID, componentName, componentKind, userID string) (*AgentResponse, error) {
	if sessionID == "" {
		return nil, &ValidationError{Message: "sessionId is required"}
	}
	if componentName == "" {
		return nil, &ValidationError{Message: "componentName is required"}
	}
	if componentKind == "" {
		return nil, &ValidationError{Message: "componentKind is required"}
	}
	if userID == "" {
		return nil, &ValidationError{Message: "user identity is required"}
	}

	lookup, err := s.lookupComponent(ctx, componentName, componentKind)
	if err != nil {
		return nil, err
	}
	resolvedKind := lookup.kind
	peerInstanceName := lookup.instanceName

	if cleanupErr := s.DeleteForSession(ctx, sessionID, userID); cleanupErr != nil {
		return nil, fmt.Errorf("prior ephemeral test agent for this session is still being torn down; please retry shortly: %w", cleanupErr)
	}

	name := generateTestAgentName(sessionID, userID)
	expiresAt := time.Now().Add(s.ttl).UnixMilli()
	description := fmt.Sprintf("Ephemeral test agent for builder session %s (%s: %s)",
		sessionID, resolvedKind, componentName)
	harnessKind := resolvedKind
	if harnessKind == ComponentKindA2AAgent {
		harnessKind = ComponentKindAgent
	}
	systemPrompt := buildTestAgentSystemPrompt(harnessKind, componentName, peerInstanceName)
	skillsJSON := "[]"
	skillRefs := []string{testHarnessSkillName}
	if resolvedKind == ComponentKindSkill {
		skillRefs = append(skillRefs, componentName)
	}
	skillRefsJSON, err := skillRefsToJSON(skillRefs)
	if err != nil {
		return nil, fmt.Errorf("build ephemeral skill_refs: %w", err)
	}

	agent := &store.Agent{
		Name:                name,
		Type:                AgentTypeStandard,
		Description:         &description,
		SystemPrompt:        &systemPrompt,
		Skills:              &skillsJSON,
		SkillRefs:           &skillRefsJSON,
		CreatedBy:           userID,
		UpdatedBy:           userID,
		IsEphemeral:         true,
		ExpiresAt:           &expiresAt,
		SourceSessionID:     &sessionID,
		SourceComponentName: &componentName,
		SourceComponentKind: &resolvedKind,
	}

	if createErr := s.agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); createErr != nil {
		if dbutil.IsUniqueViolation(createErr) {
			return nil, &ConflictError{
				Message:    testAgentNameInUseMessage,
				LogMessage: fmt.Sprintf("create ephemeral agent: %v", createErr),
			}
		}
		return nil, fmt.Errorf("create ephemeral agent: %w", createErr)
	}

	var peerAllowList []string
	if peerInstanceName != "" {
		peerAllowList = []string{peerInstanceName}
	}

	_, err = s.deploymentSvc.CreateWithOpts(ctx, CreateAgentDeploymentRequest{
		AgentID: agent.ID,
		Action:  DeployActionDeploy,
	}, userID, DeployOptions{
		TestMode:      true,
		UserID:        userID,
		PeerAllowList: peerAllowList,
	})
	if err != nil {
		if delErr := s.agentRepo.Delete(ctx, agent.ID); delErr != nil {
			s.logger.Error("rollback ephemeral agent after deploy failure",
				"agentID", agent.ID, "error", delErr)
		}
		return nil, fmt.Errorf("deploy ephemeral agent: %w", err)
	}

	return s.agentSvc.GetByID(ctx, agent.ID)
}

func (s *TestAgentService) GetForSession(ctx context.Context, sessionID, userID string) (*AgentResponse, error) {
	a, err := s.agentRepo.GetEphemeralForSession(ctx, sessionID, userID)
	if err != nil {
		return nil, fmt.Errorf("get ephemeral agent: %w", err)
	}
	if a == nil {
		return nil, nil
	}
	return s.agentSvc.GetByID(ctx, a.ID)
}

type ParentLookup struct {
	BuilderSessionID    string
	ComponentName       string
	ComponentKind       string
	TestAgentName       string
	TestAgentInstanceID string
}

func (s *TestAgentService) LookupParentByTestAgentName(ctx context.Context, agentName, userID string) (*ParentLookup, error) {
	if agentName == "" || userID == "" {
		return nil, &ValidationError{Message: "agent name and userId are required"}
	}
	a, err := s.lookupEphemeralAgentByEitherName(ctx, agentName)
	if err != nil {
		return nil, fmt.Errorf("look up test agent: %w", err)
	}
	if a == nil || !a.IsEphemeral || a.CreatedBy != userID {
		return nil, nil
	}
	if a.SourceSessionID == nil || a.SourceComponentName == nil {
		return nil, nil
	}
	kind := ""
	if a.SourceComponentKind != nil {
		kind = *a.SourceComponentKind
	}
	return &ParentLookup{
		BuilderSessionID:    *a.SourceSessionID,
		ComponentName:       *a.SourceComponentName,
		ComponentKind:       kind,
		TestAgentName:       a.Name,
		TestAgentInstanceID: a.ID,
	}, nil
}

func (s *TestAgentService) lookupEphemeralAgentByEitherName(ctx context.Context, name string) (*store.Agent, error) {
	if a2a.IsEphemeralTestAgentName(name) {
		a, _, _, _, _, err := s.agentRepo.GetByName(ctx, name)
		return a, err
	}
	if id, ok := agentIDFromInstanceName(name); ok {
		a, _, _, _, _, err := s.agentRepo.GetByID(ctx, id)
		return a, err
	}
	return nil, nil
}

func agentIDFromInstanceName(name string) (string, bool) {
	id, kind, ok := a2a.IDFromInstanceName(name)
	if !ok || kind != a2a.KindAgent {
		return "", false
	}
	return id, true
}

func (s *TestAgentService) DeleteForSession(ctx context.Context, sessionID, userID string) error {
	a, err := s.agentRepo.GetEphemeralForSession(ctx, sessionID, userID)
	if err != nil {
		return fmt.Errorf("look up ephemeral agent: %w", err)
	}
	if a == nil {
		return nil
	}
	return s.tearDown(ctx, a.ID, userID)
}

func (s *TestAgentService) PurgeExpired(ctx context.Context) (int, error) {
	now := time.Now().UnixMilli()
	ids, err := s.agentRepo.ListExpiredEphemeralIDs(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("list expired ephemeral agents: %w", err)
	}
	reaped := 0
	for _, id := range ids {
		if err := s.tearDown(ctx, id, systemUserIDSweeper); err != nil {
			s.logger.Error("ephemeral agent purge failed", "agentID", id, "error", err)
			continue
		}
		reaped++
	}
	if reaped > 0 {
		s.logger.Debug("ephemeral test agents purged", "count", reaped)
	}
	return reaped, nil
}

func (s *TestAgentService) tearDown(ctx context.Context, agentID, actorUserID string) error {
	mayBeLive, err := s.agentRepo.HasPossiblyLiveDeployment(ctx, agentID)
	if err != nil {
		return fmt.Errorf("check for a live deployment %s: %w", agentID, err)
	}
	if mayBeLive {
		if err := s.deploymentSvc.Undeploy(ctx, agentID, actorUserID); err != nil {
			return fmt.Errorf("undeploy ephemeral agent %s (DB row kept for retry): %w", agentID, err)
		}
	}
	if err := s.agentRepo.Delete(ctx, agentID); err != nil {
		return fmt.Errorf("delete ephemeral agent: %w", err)
	}
	return nil
}

type componentLookup struct {
	kind         string
	instanceName string
}

func (s *TestAgentService) lookupComponent(ctx context.Context, name, requestedKind string) (componentLookup, error) {
	switch requestedKind {
	case ComponentKindSkill:
		if s.skillRepo == nil {
			return componentLookup{}, &ValidationError{Message: "skill testing is not configured on this platform"}
		}
		if name == testHarnessSkillName {
			return componentLookup{}, &ValidationError{Message: fmt.Sprintf("cannot use %q as the component under test — it is the test harness itself", name)}
		}
		sk, err := s.skillRepo.GetByName(ctx, name)
		if err != nil {
			return componentLookup{}, fmt.Errorf("look up skill: %w", err)
		}
		if sk == nil {
			return componentLookup{}, &NotFoundError{Resource: "skill", ID: name}
		}
		if sk.DiscoveryStatus != "ready" {
			return componentLookup{}, &ValidationError{Message: fmt.Sprintf("skill %q is not ready for testing (status: %s)", name, sk.DiscoveryStatus)}
		}
		return componentLookup{kind: ComponentKindSkill, instanceName: ""}, nil
	case ComponentKindA2AAgent:
		a, _, _, _, _, err := s.agentRepo.GetByName(ctx, name)
		if err != nil {
			return componentLookup{}, fmt.Errorf("look up remote agent: %w", err)
		}
		if a == nil {
			return componentLookup{}, &NotFoundError{Resource: "remote agent", ID: name}
		}
		if a.IsEphemeral {
			return componentLookup{}, &ValidationError{Message: fmt.Sprintf("cannot create a test agent that wraps another ephemeral agent: %q", name)}
		}
		return componentLookup{kind: ComponentKindA2AAgent, instanceName: AgentInstanceName(a)}, nil
	case ComponentKindAgent, ComponentKindWorkflow:
	default:
		return componentLookup{}, &ValidationError{Message: fmt.Sprintf("unknown component kind: %q", requestedKind)}
	}

	if requestedKind == ComponentKindWorkflow && s.workflowRepo == nil {
		return componentLookup{}, &ValidationError{Message: "workflow testing is not configured on this platform"}
	}
	if s.workflowRepo != nil && requestedKind != ComponentKindAgent {
		wf, err := s.workflowRepo.GetByName(ctx, name)
		if err != nil {
			return componentLookup{}, fmt.Errorf("look up workflow: %w", err)
		}
		if wf != nil {
			return componentLookup{
				kind:         ComponentKindWorkflow,
				instanceName: a2a.InstanceName(a2a.KindWorkflow, wf.ID),
			}, nil
		}
	}
	if requestedKind == ComponentKindWorkflow {
		return componentLookup{}, &NotFoundError{Resource: "workflow", ID: name}
	}
	a, _, _, _, _, err := s.agentRepo.GetByName(ctx, name)
	if err != nil {
		return componentLookup{}, fmt.Errorf("look up agent: %w", err)
	}
	if a == nil {
		return componentLookup{}, &NotFoundError{Resource: "component", ID: name}
	}
	if a.IsEphemeral {
		return componentLookup{}, &ValidationError{Message: fmt.Sprintf("cannot create a test agent that wraps another ephemeral agent: %q", name)}
	}
	return componentLookup{
		kind:         ComponentKindAgent,
		instanceName: AgentInstanceName(a),
	}, nil
}

func skillRefsToJSON(names []string) (string, error) {
	if len(names) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(names)
	if err != nil {
		return "", fmt.Errorf("marshal skill refs: %w", err)
	}
	return string(b), nil
}

func generateTestAgentName(sessionID, userID string) string {
	wholeSessionIDPart := strings.ToLower(strings.ReplaceAll(sessionID, "-", ""))
	userHash := sha256.Sum256([]byte(userID))
	return a2a.EphemeralTestAgentNamePrefix + wholeSessionIDPart + "__" + hex.EncodeToString(userHash[:testAgentUserHashBytes])
}

func buildTestAgentSystemPrompt(componentKind, componentName, peerInstanceName string) string {
	noun := "agent"
	var entryParagraph, componentSection, planExtraArg, planNoticeException, recommendationsExtraArg, retryDescriptionBullet string
	reportedInput := "the exact input you invoked the wrapped agent with"
	switch componentKind {
	case ComponentKindWorkflow:
		noun = "workflow"
		reportedInput = "the exact input you invoked the wrapped workflow with"
		toolName := a2a.WorkflowToolName(peerInstanceName)
		componentSection = workflowInputSection(toolName) + declaredContractSection(toolName)
		planNoticeException = workflowPlanNoticeException
		planExtraArg = ",\n  \"inputSchema\": <the JSON Schema described under \"Workflow input schema\" below>" + planDescriptionArg
		recommendationsExtraArg = recommendationsDescriptionArg
		retryDescriptionBullet = retryDescriptionBulletText
		entryParagraph = fmt.Sprintf(
			"You have access to the test_harness skill (generate_test_plan + "+
				"generate_recommendations) and one peer/workflow tool that calls "+
				"%[1]q. The exact name of that tool is %[2]q — call it under that "+
				"exact name, not under %[1]q or any other variation. (Peer tools "+
				"are addressed by the wrapped workflow's broker instance name, which "+
				"is a UUID-derived form, not its display name.) Use the skill's "+
				"tools to drive structured testing — do not invent test data or "+
				"recommendations inline in chat. You have no other peers, builder "+
				"access, or deployment tools.",
			componentName, toolName)
	case ComponentKindSkill:
		noun = "skill"
		reportedInput = "the ask you acted on: the plan's sampleInput, or the user's edited version of it"
		entryParagraph = fmt.Sprintf(
			"You have access to the test_harness skill (generate_test_plan + "+
				"generate_recommendations) AND to the skill under test, %[1]q, "+
				"whose bundled tools are loaded directly on this ephemeral. To "+
				"exercise the skill, call its bundled tools the way a real agent "+
				"would; their names are namespaced under %[1]q (e.g. "+
				"%[1]q__<tool>). You have no peers, builder access, or deployment "+
				"tools. Use the test_harness skill's tools to drive structured "+
				"testing — do not invent test data or recommendations inline in "+
				"chat.",
			componentName)
	default:
		toolName := "peer_" + peerInstanceName
		componentSection = declaredContractSection(toolName)
		planNoticeException = agentPlanNoticeException
		planExtraArg = planDescriptionArg
		recommendationsExtraArg = recommendationsDescriptionArg
		retryDescriptionBullet = retryDescriptionBulletText
		entryParagraph = fmt.Sprintf(
			"You have access to the test_harness skill (generate_test_plan + "+
				"generate_recommendations) and one peer/workflow tool that calls "+
				"%[1]q. The exact name of that tool is %[2]q — call it under that "+
				"exact name, not under %[1]q or any other variation. (Peer tools "+
				"are addressed by the wrapped agent's broker instance name, which "+
				"is a UUID-derived form, not its display name.) Use the skill's "+
				"tools to drive structured testing — do not invent test data or "+
				"recommendations inline in chat. You have no other peers, builder "+
				"access, or deployment tools.",
			componentName, toolName)
	}
	return fmt.Sprintf(
		"You are an ephemeral test harness for the %[1]s named %[2]q.\n\n"+
			"%[4]s\n\n"+
			"## CRITICAL: parameters for test_harness tools\n"+
			"\n"+
			"The wrapped component is fixed for this test agent. Every call to "+
			"sam-test-harness__generate_test_plan and "+
			"sam-test-harness__generate_recommendations MUST pass these exact "+
			"values, copied verbatim:\n"+
			"\n"+
			"  componentName: %[2]q\n"+
			"  componentKind: %[3]q\n"+
			"\n"+
			"Worked example for generate_test_plan — call it like this every time:\n"+
			"\n"+
			"```json\n"+
			"{\n"+
			"  \"componentName\": %[2]q,\n"+
			"  \"componentKind\": %[3]q%[6]s\n"+
			"}\n"+
			"```\n"+
			"\n"+
			"Do NOT omit componentName or componentKind. Do NOT substitute different values "+
			"(e.g. a description of the component). Do NOT ask the user to "+
			"supply them — they are part of the test agent's identity, not "+
			"user input. If the tool returns 'componentName is required', "+
			"you forgot to include the field in the JSON args; retry with "+
			"the same arguments as the example above, every field included.\n\n"+
			"%[5]s"+
			"## Required args for generate_recommendations\n"+
			"\n"+
			"generate_recommendations needs more than the name and kind. After you run "+
			"the wrapped %[1]s and capture its output, your tool call MUST "+
			"include every one of these fields:\n"+
			"\n"+
			"```json\n"+
			"{\n"+
			"  \"componentName\": %[2]q,\n"+
			"  \"componentKind\": %[3]q,\n"+
			"  \"sampleInput\":    <%[7]s>,\n"+
			"  \"expectedOutput\": <copy verbatim from the test plan>,\n"+
			"  \"actualOutput\":   <copy verbatim from the wrapped %[1]s's tool result>%[9]s\n"+
			"}\n"+
			"```\n"+
			"\n"+
			"sampleInput is the input of the run you just made: the report "+
			"shows it next to the output, so it must be exactly what the run "+
			"used, not a paraphrase, and not the plan's original if the user "+
			"edited it before starting the test. "+
			"expectedOutput is in the most recent "+
			"sam-test-harness__generate_test_plan result (or the plan the user "+
			"reviewed in the run message) — re-read it from conversation "+
			"history if you need to. actualOutput is the value "+
			"the wrapped %[1]s's tool just returned for you to record. The "+
			"three content fields are arbitrary JSON (string, object, array) "+
			"— preserve the shape the test plan and wrapped %[1]s produced; "+
			"do not stringify or truncate.\n"+
			"\n"+
			"These errors, and ONLY these errors, mean you omitted a "+
			"required argument. When you see one, do not give up and don't "+
			"reply 'run finished' — re-issue the SAME call with the missing "+
			"field added:\n"+
			"  - 'sampleInput is required'\n"+
			"  - 'expectedOutput is required'\n"+
			"  - 'actualOutput is required'\n"+
			"  - 'componentName is required'\n"+
			"  - 'componentKind must be ...'\n"+
			"%[10]s"+
			"\n"+
			"Any other tool failure is NOT a missing argument and must NOT be "+
			"retried. In particular, a tool result carrying \"status\": "+
			"\"failed\" or \"retryable\": false — including TOOL_TIMEOUT, "+
			"TOOL_EXIT_NONZERO, TOOL_SIGNALED and TOOL_EXEC_FAILED — is an "+
			"infrastructure failure that will fail identically on a retry. Do "+
			"not re-issue the call, do not ask the user to retry, and do not "+
			"describe it as a temporary or transient service problem. State "+
			"plainly which tool failed and what the error said, and that it "+
			"needs an operator to investigate.\n"+
			"\n"+
			"Only after generate_recommendations succeeds may you reply with "+
			"a short acknowledgement.\n\n"+
			"## Do NOT duplicate tool output in chat\n"+
			"\n"+
			"The results of sam-test-harness__generate_test_plan and "+
			"sam-test-harness__generate_recommendations are rendered in the UI "+
			"as inline cards (an editable test plan card and a structured "+
			"test report card with a Recommendations sub-section). The user "+
			"sees those cards alongside your text reply.\n"+
			"\n"+
			"After EITHER tool returns successfully, reply with a SINGLE "+
			"SHORT SENTENCE (≤15 words) that just acknowledges the next "+
			"step. Do NOT, under any circumstance:\n"+
			"  - restate the sample input, expected output, or actual output,\n"+
			"  - enumerate, paraphrase, or summarise the recommendations,\n"+
			"  - list \"high / medium / low priority\" items,\n"+
			"  - explain what each recommendation means,\n"+
			"  - paste, quote, or describe the JSON the tool returned.\n"+
			"\n"+
			"Acceptable replies after generate_test_plan:\n"+
			"  \"Test plan ready — review the plan card and click Start Test.\"\n"+
			"\n"+
			"%[8]s"+
			"Acceptable replies after generate_recommendations:\n"+
			"  \"Run finished — see the report card for findings.\"\n"+
			"  \"All good — no issues found in this run.\"\n"+
			"\n"+
			"That's the whole reply. Do not append further analysis. The "+
			"user can read the cards.\n\n"+
			"## Do NOT speculate about session contents you can't see\n"+
			"\n"+
			"You have no list-artifacts, list-files, or filesystem tool. "+
			"You CANNOT see the artifact store, uploaded files, or any "+
			"prior runs' outputs. If the user asks \"what artifacts do "+
			"you see\", \"what's in the session\", or anything similar, "+
			"do not claim the store is empty or list what's there — both "+
			"answers would be made up. Reply that you don't have a tool "+
			"for listing the session contents and offer the next concrete "+
			"action (run a test, generate a plan, etc.).",
		noun, componentName, componentKind, entryParagraph, componentSection, planExtraArg, reportedInput, planNoticeException, recommendationsExtraArg, retryDescriptionBullet)
}

const retryDescriptionBulletText = "  - 'description is required ...' (generate_test_plan only; set it to the wrapped tool's description)\n"

const (
	planDescriptionArg = ",\n  \"description\": <the wrapped tool's description, copied exactly as it is declared>"

	recommendationsDescriptionArg = ",\n  \"description\": <the wrapped tool's description as you see it now, copied exactly>"
)

func declaredContractSection(wrappedToolName string) string {
	return fmt.Sprintf(
		"## What the component declares it returns\n"+
			"\n"+
			"The expected output of a test comes only from what the wrapped component "+
			"declares, never from its name. The declaration is the description of "+
			"your %[1]q tool as you see it in your tool list, including the "+
			"\"Declared output fields\" a workflow's description ends with. Pass "+
			"that description, copied exactly and in full, as \"description\" in "+
			"every call to generate_test_plan and generate_recommendations. Do "+
			"NOT reword it, summarise it, or add anything to it, and do NOT "+
			"write an expected output yourself. A tool that declares nothing still "+
			"has its opening sentence: pass it as it is, and the plan then says the "+
			"expected output is unspecified and the run is judged only on completing.\n\n",
		wrappedToolName)
}

const agentPlanNoticeException = "One exception: the generate_test_plan result message may contain " +
	"\"NOTE\" saying the component declares nothing about what it returns, " +
	"so the expected output is unspecified. The card shows it, but say it in " +
	"your one sentence too, e.g. \"Plan ready — this agent declares no expected " +
	"output, so the run will only be checked for completing.\" Do not call " +
	"generate_test_plan again because of it.\n" +
	"\n"

const workflowPlanNoticeException = "One exception: the generate_test_plan result message may contain " +
	"\"NOTE\" (the sample input was not checked against the " +
	"workflow's input schema) or \"WARNING\" (it does not match that " +
	"schema). The card cannot show either, so say it in your one " +
	"sentence, e.g. \"Plan ready, but its sample input may not match " +
	"the workflow — check it before starting.\" A NOTE may also say the " +
	"workflow declares nothing about what it returns, so the expected output " +
	"is unspecified; the card shows that, but say it in your one sentence too. " +
	"Do not call generate_test_plan again just because of a NOTE.\n" +
	"\n"

func workflowInputSection(workflowToolName string) string {
	return fmt.Sprintf(
		"## Workflow input schema\n"+
			"\n"+
			"The wrapped workflow takes a typed input, declared as the parameters "+
			"of your %[1]q tool. When you call generate_test_plan, ALSO pass "+
			"inputSchema: a JSON Schema object of the form "+
			"{\"type\": \"object\", \"properties\": {...}, \"required\": [...]} "+
			"whose properties copy, exactly as declared, every parameter of that "+
			"tool except input_artifact (copy nested definitions inline; the "+
			"tool does not declare which parameters are required), and whose "+
			"required lists all of those property names. Rebuild it from the tool's current definition on "+
			"every call — the workflow may have been edited since an earlier "+
			"test. The tool uses it to generate a sampleInput the workflow "+
			"accepts.\n"+
			"\n"+
			"When you run the test, call %[1]q with each field of the plan's "+
			"sampleInput as the argument of the same name and the same value. "+
			"Do NOT rename, remap, drop, add, or reinterpret fields to make the "+
			"input fit, whatever the plan says. The plan was checked against the "+
			"workflow's input schema unless its result carried a NOTE about the input "+
			"schema or a WARNING, or the user edited it.\n\n",
		workflowToolName)
}
