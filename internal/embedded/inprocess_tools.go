// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !sam_lean

package embedded

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	samtoolsdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"

	bedrockkb "github.com/SolaceProducts/solace-agent-mesh/tools/go/bedrock-kb/bedrockkb"
	buildapply "github.com/SolaceProducts/solace-agent-mesh/tools/go/build_apply/buildapply"
	concataudio "github.com/SolaceProducts/solace-agent-mesh/tools/go/concatenate_audio/concataudio"
	converttomarkdown "github.com/SolaceProducts/solace-agent-mesh/tools/go/convert_to_markdown/converttomarkdown"
	documentdb "github.com/SolaceProducts/solace-agent-mesh/tools/go/document-db/documentdb"
	docxtool "github.com/SolaceProducts/solace-agent-mesh/tools/go/docx/docxtool"
	eventmesh "github.com/SolaceProducts/solace-agent-mesh/tools/go/event-mesh/eventmesh"
	eventmeshprofile "github.com/SolaceProducts/solace-agent-mesh/tools/go/event_mesh_profile_provider/eventmeshprofile"
	ffmpegtool "github.com/SolaceProducts/solace-agent-mesh/tools/go/ffmpeg/ffmpegtool"
	graphdb "github.com/SolaceProducts/solace-agent-mesh/tools/go/graph-db/graphdb"
	htmltopdf "github.com/SolaceProducts/solace-agent-mesh/tools/go/html_to_pdf/htmltopdf"
	imagemagicktool "github.com/SolaceProducts/solace-agent-mesh/tools/go/imagemagick/imagemagicktool"
	libreofficetool "github.com/SolaceProducts/solace-agent-mesh/tools/go/libreoffice/libreofficetool"
	mermaidtool "github.com/SolaceProducts/solace-agent-mesh/tools/go/mermaid/mermaidtool"
	multispeakertts "github.com/SolaceProducts/solace-agent-mesh/tools/go/multi_speaker_text_to_speech/multispeakertts"
	pdfextracttext "github.com/SolaceProducts/solace-agent-mesh/tools/go/pdf_extract_text/pdfextracttext"
	pdfops "github.com/SolaceProducts/solace-agent-mesh/tools/go/pdf_ops/pdfops"
	"github.com/SolaceProducts/solace-agent-mesh/tools/go/plotly/plotlytool"
	pptxtool "github.com/SolaceProducts/solace-agent-mesh/tools/go/pptx/pptxtool"
	rendertoimages "github.com/SolaceProducts/solace-agent-mesh/tools/go/render_to_images/rendertoimages"
	samcoder "github.com/SolaceProducts/solace-agent-mesh/tools/go/sam_coder/coder"
	searchtool "github.com/SolaceProducts/solace-agent-mesh/tools/go/search/searchtool"
	sendemail "github.com/SolaceProducts/solace-agent-mesh/tools/go/send_email/sendemail"
	slacktool "github.com/SolaceProducts/solace-agent-mesh/tools/go/slack/slacktool"
	sqltool "github.com/SolaceProducts/solace-agent-mesh/tools/go/sql/sqltool"
	testharness "github.com/SolaceProducts/solace-agent-mesh/tools/go/test_harness/testharness"
	texttospeech "github.com/SolaceProducts/solace-agent-mesh/tools/go/text_to_speech/texttospeech"
	websearchtool "github.com/SolaceProducts/solace-agent-mesh/tools/go/web_search/websearchtool"
	xlsxtool "github.com/SolaceProducts/solace-agent-mesh/tools/go/xlsx/xlsxtool"
)

func init() {
	str.RegisterInProcessTools(inProcessToolDefs()...)
}

func inProcessToolDefs() []*samtoolsdk.ToolDef {
	var defs []*samtoolsdk.ToolDef
	defs = append(defs, sqltool.Tools()...)
	defs = append(defs, bedrockkb.Tools()...)
	defs = append(defs, documentdb.Tools()...)
	defs = append(defs, graphdb.Tools()...)
	defs = append(defs, searchtool.Tools()...)
	defs = append(defs, websearchtool.Tools()...)
	defs = append(defs, slacktool.Tools()...)
	defs = append(defs, sendemail.Tools()...)
	defs = append(defs, eventmesh.Tools()...)
	defs = append(defs, eventmeshprofile.Tools()...)
	defs = append(defs, buildapply.Tools()...)
	defs = append(defs, concataudio.Tools()...)
	defs = append(defs, converttomarkdown.Tools()...)
	defs = append(defs, ffmpegtool.Tools()...)
	defs = append(defs, htmltopdf.Tools()...)
	defs = append(defs, imagemagicktool.Tools()...)
	defs = append(defs, libreofficetool.Tools()...)
	defs = append(defs, mermaidtool.Tools()...)
	defs = append(defs, multispeakertts.Tools()...)
	defs = append(defs, pdfextracttext.Tools()...)
	defs = append(defs, pdfops.Tools()...)
	defs = append(defs, plotlytool.Tools()...)
	defs = append(defs, pptxtool.Tools()...)
	defs = append(defs, rendertoimages.Tools()...)
	defs = append(defs, texttospeech.Tools()...)
	defs = append(defs, xlsxtool.Tools()...)
	defs = append(defs, docxtool.Tools()...)
	defs = append(defs, testharness.Tools()...)
	defs = append(defs, samcoder.Tools()...)
	return defs
}
