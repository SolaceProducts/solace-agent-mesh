// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package config

const (
	KeyNamespace        = "namespace"
	KeyAppConfig        = "app_config"
	KeyAgentName        = "agent_name"
	KeyWorkerID         = "worker_id"
	KeyVolumeRoot       = "volume_root"
	KeyVolumeDBURL      = "volume_database_url"
	KeySkillsBasePath   = "skills_base_path"
	KeySkillsDir        = "skills_dir"
	KeyBuiltinSkillsDir = "builtin_skills_dir"
	KeyBuiltinToolsDir  = "builtin_tools_dir"
	KeyToolModelAlias   = "tool_model_alias"
	KeyToolsetsDir      = "toolsets_dir"
	KeyToolsDir         = "tools_dir"
	KeyPackagesDir      = "packages_dir"
)

const (
	DefaultNamespace                = "solace-agent-mesh"
	DefaultRequestTimeoutSeconds    = 300
	DefaultCardPublishIntervalSecs  = 60
	DefaultDiscoveryIntervalSeconds = 60

	DefaultPeerRequestTimeoutSecs     = 120
	DefaultWorkflowRequestTimeoutSecs = 600
	DefaultSubTaskTimeoutSecs         = 600

	DefaultWorkerID = "builtin-tools-worker"
)
