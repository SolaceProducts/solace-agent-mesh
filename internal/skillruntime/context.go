// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

type SkillContext struct {
	Name string

	LocalPath string

	InstructionContent string

	Description string
	Tags        []string
	Examples    []string

	RequiredBuiltins []string

	Tools []map[string]any

	Toolsets []string

	BundledTools []map[string]any

	HasReferences bool
	HasAssets     bool

	HasTemplates bool
	Templates    []TemplateInfo

	ResourceCount int

	ContentHash string
}
