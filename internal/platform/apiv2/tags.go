// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

const (
	groupAgents            = "Agents"
	groupWorkflows         = "Workflows"
	groupConnectors        = "Connectors"
	groupEntrypoints       = "Entrypoints"
	groupModels            = "Models"
	groupToolsetsAndSkills = "Toolsets and Skills"
	groupAccessControl     = "Access Control"
	groupBudgetsAndUsage   = "Budgets and Usage"
	groupPlatform          = "Platform"
	groupEvaluations       = "Evaluations"
)

var (
	agentDeploymentsTag       = Tag{Name: "Agent Deployments", Group: groupAgents, Text: "Deploy and undeploy agents, and read the outcome of each deployment."}
	agentPlatformDefaultsTag  = Tag{Name: "Agent Platform Defaults", Group: groupAgents, Text: "Read the defaults every agent starts from."}
	agentsTag                 = Tag{Name: "Agents", Group: groupAgents, Text: "Manage the agents that Agent Mesh builds."}
	apiKeysTag                = Tag{Name: "API Keys", Group: groupAccessControl, Text: "Manage the API keys that let a pipeline call the management API as a service account."}
	budgetAssignmentsTag      = Tag{Name: "Budget Assignments", Group: groupBudgetsAndUsage, Text: "Choose which budget applies to each user, in place of their tier's default."}
	budgetSubjectsTag         = Tag{Name: "Budget Subjects", Group: groupBudgetsAndUsage, Text: "Read the budget that applies to each user and to the mesh, and raise it for the rest of the current budget period with a top-up."}
	budgetsTag                = Tag{Name: "Budgets", Group: groupBudgetsAndUsage, Text: "Manage the spending limits that apply to the whole mesh and to each user."}
	connectorSchemasTag       = Tag{Name: "Connector Schemas", Group: groupConnectors, Text: "Read the values each connector type accepts."}
	connectorsTag             = Tag{Name: "Connectors", Group: groupConnectors, Text: "Manage the connectors that give agents and workflows access to external systems."}
	entrypointDeploymentsTag  = Tag{Name: "Entrypoint Deployments", Group: groupEntrypoints, Text: "Deploy and undeploy entrypoints, and read the outcome of each deployment."}
	entrypointSchemasTag      = Tag{Name: "Entrypoint Schemas", Group: groupEntrypoints, Text: "Read the values each entrypoint type accepts."}
	entrypointsTag            = Tag{Name: "Entrypoints", Group: groupEntrypoints, Text: "Manage the entrypoints that connect people and systems to Agent Mesh, such as Slack, Teams, webhooks, and WhatsApp."}
	eventCatalogEntriesTag    = Tag{Name: "Event Catalog Entries", Group: groupEntrypoints, Text: "Manage the event catalog: the named business events, each on a topic of an event mesh entrypoint's broker, that the owner of an always-on agent picks from."}
	healthTag                 = Tag{Name: "Health", Group: groupPlatform, Text: "Check that the platform is running."}
	linkAgentsTag             = Tag{Name: "Link Agents", Group: groupAgents, Text: "Manage the third-party agents that join the mesh by connecting to an agent_link entrypoint, and their tokens."}
	modelStatusTag            = Tag{Name: "Model Status", Group: groupModels, Text: "Read whether the platform has a model configured."}
	modelsTag                 = Tag{Name: "Models", Group: groupModels, Text: "Manage the large language model (LLM) configurations that agents and workflows run on, see what uses each one, and probe a provider before saving one."}
	productAnalyticsTag       = Tag{Name: "Product Analytics", Group: groupPlatform, Text: "Send usage reports from the command line."}
	profileProviderTag        = Tag{Name: "Profile Provider", Group: groupPlatform, Text: "Set the service that supplies user profiles."}
	providersTag              = Tag{Name: "Providers", Group: groupModels, Text: "Read the generation parameters and models of an LLM provider."}
	rbacTag                   = Tag{Name: "Access Control", Group: groupAccessControl, Text: "Manage the roles, grants, claim mappings, default roles, and service accounts that decide what each caller can do, and read the scope catalog."}
	remoteAgentsTag           = Tag{Name: "Remote Agents", Group: groupAgents, Text: "Manage the remote agents that Agent Mesh reaches over the Agent-to-Agent (A2A) protocol."}
	serverCapabilitiesTag     = Tag{Name: "Server Capabilities", Group: groupAccessControl, Text: "Read what this server lets callers do."}
	serviceAccountToolAuthTag = Tag{Name: "Service Account Tool Authorization", Group: groupAccessControl, Text: "Connect the tool credentials an agent holds for a custom service account, so a run with no person present, such as a scheduled task, can call a tool that signs in through a browser."}
	skillsTag                 = Tag{Name: "Skills", Group: groupToolsetsAndSkills, Text: "Manage the skills that give agents instructions, reference files, and tools: upload or download a skill's bundle, set the settings every agent gets, and see which agents use each skill."}
	supportedModelsTag        = Tag{Name: "Supported Models", Group: groupModels, Text: "List the models a provider serves for the credentials sent."}
	toolsetBuildTargetTag     = Tag{Name: "Toolset Build Target", Group: groupToolsetsAndSkills, Text: "Read the platform to build toolset binaries for."}
	toolsetUploadsTag         = Tag{Name: "Toolset Uploads", Group: groupToolsetsAndSkills, Text: "Upload a toolset bundle in chunks."}
	toolsetsTag               = Tag{Name: "Toolsets", Group: groupToolsetsAndSkills, Text: "Manage the toolsets that give agents their tools: upload a bundle of tools, set the settings every agent gets, and see which agents use each toolset."}
	usageTag                  = Tag{Name: "Usage", Group: groupBudgetsAndUsage, Text: "Read what the deployment's LLM calls cost, by user, agent, workflow and model."}
	userUsageTag              = Tag{Name: "User Usage", Group: groupBudgetsAndUsage, Text: "Read what the caller's own LLM calls cost."}
	usersTag                  = Tag{Name: "Users", Group: groupAccessControl, Text: "Read the users known to the platform, and manage the roles granted to each, their identity claims, and the API keys they own."}
	versionTag                = Tag{Name: "Version", Group: groupPlatform, Text: "Read the platform version."}
	webuiSettingsTag          = Tag{Name: "Web UI Settings", Group: groupPlatform, Text: "Read and change the settings of the web UI."}
	workflowDeploymentsTag    = Tag{Name: "Workflow Deployments", Group: groupWorkflows, Text: "Deploy and undeploy workflows, and read the outcome of each deployment."}
	workflowsTag              = Tag{Name: "Workflows", Group: groupWorkflows, Text: "Manage the workflows that run agents through a defined sequence of steps."}
	datasetsTag               = Tag{Name: "Evaluation Datasets", Group: groupEvaluations, Text: "Manage the datasets of examples that experiments run agents against."}
	evaluatorsTag             = Tag{Name: "Evaluation Evaluators", Group: groupEvaluations, Text: "Manage the evaluators that score the answers an agent gives in an experiment."}
	examplesTag               = Tag{Name: "Evaluation Examples", Group: groupEvaluations, Text: "Manage the examples in an evaluation dataset: each prompt with its expected answer and files, added one at a time or imported and exported in bulk."}
	experimentsTag            = Tag{Name: "Evaluation Experiments", Group: groupEvaluations, Text: "Define experiments that run an agent against a dataset's examples and score its answers with evaluators."}
	runResultsTag             = Tag{Name: "Evaluation Run Results", Group: groupEvaluations, Text: "Read the outcome of each trial of an evaluation run: the answer, the scores, the token use, and the files, messages, and events captured along the way."}
	runsTag                   = Tag{Name: "Evaluation Runs", Group: groupEvaluations, Text: "Start an experiment, follow its runs, read each run's status and scores, and cancel a run."}
	toolAuthTag               = Tag{Name: "Evaluation Tool Authorization", Group: groupEvaluations, Text: "Connect the tool credentials an experiment's runs use, so tools that sign in through a browser work in an evaluation."}
	watchlistTag              = Tag{Name: "Evaluation Watchlist", Group: groupEvaluations, Text: "Pin the agents and workflows whose evaluation scores you follow, and chart how those scores change from run to run."}
)
