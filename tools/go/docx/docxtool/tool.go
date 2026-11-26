// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docxtool

import (
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("docx_create_document",
			"Creates a Word (.docx) document from a saved content-spec artifact. Save the blocks array as a YAML or JSON artifact first (via save_artifact or a save_artifact fenced block), then pass its filename as the `content` parameter. Supports headings, formatted paragraphs, bullet/numbered lists, tables, images, page breaks, and a real Word TOC field. To turn raw Markdown into a document, map its structure onto these blocks (headings -> heading, paragraphs -> paragraph, bullet groups -> bullet_list, tables -> table) rather than dumping it verbatim as one text block.",
			createDocument,
			sdk.WithInstructions(createDocumentInstructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

const createDocumentInstructions = `The block types, their fields and an example are in the sam-docx skill instructions. Save the blocks array as a YAML or JSON artifact in a save block first, then pass its filename as ` + "`content`" + ` (add ` + "`:N`" + ` to pin a version). Images go in the top-level ` + "`images`" + ` list and a block references one by quoted index (` + "`image_ref: \"0\"`" + `); the printable area is 6.5 x 9 in and larger images are scaled to fit. A ` + "`partial`" + ` result means some blocks became placeholders, so tell the user.`
