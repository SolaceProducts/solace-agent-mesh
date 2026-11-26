// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (CreateSkillRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateSkillRequest is what config apply creates a skill from. It is not a
copy of apiv2.CreateSkillRequest, a multipart/form-data body whose name
comes from the bundle's SKILL.md: Name is the skill the bundle holds, and
Description goes in the body's description part.`,
	}
}

func (UpdateSkillRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateSkillRequest is config apply's copy of apiv2.UpdateSkillRequest, a
PATCH body. The skill's ID goes in the path, not the body. The name comes
from the bundle's SKILL.md, and the server rejects it on PATCH.`,
	}
}

func (SkillResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SkillResponse is config apply's copy of apiv2.Skill. Name and Tags are
set in the bundle's SKILL.md, so they are the author's, not read-only.`,
	}
}
