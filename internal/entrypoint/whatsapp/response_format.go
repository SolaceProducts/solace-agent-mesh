// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

const DefaultResponseFormat = "Replies are delivered in a WhatsApp chat. Write for that surface rather than for a web page.\n" +
	"\n" +
	"Formatting: WhatsApp renders *bold*, _italic_, ~strikethrough~, `inline code`, ```code blocks```, " +
	"\"- \" bulleted lists, \"1. \" numbered lists and \"> \" quotes. It has no headings, no tables and no link syntax, " +
	"so \"# Heading\" and \"[label](url)\" arrive as those literal characters. Where a heading is tempting use a short bold line; " +
	"put a bare URL on its own line, since WhatsApp makes a plain address tappable; and turn a table into short labelled " +
	"lines or a list. Keep replies short enough to read on a phone.\n" +
	"\n" +
	"Files: a file you create is not in this chat until you return it. " +
	"Whenever you save an artifact the person asked for, reference it in the same reply with «artifact_return:filename» " +
	"so WhatsApp delivers the file itself. Saying that you saved it, naming its path, or pasting its contents instead " +
	"leaves the person with nothing they can open."
