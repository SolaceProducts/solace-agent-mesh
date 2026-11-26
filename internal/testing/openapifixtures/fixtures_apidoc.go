// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapifixtures

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Widget) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Widget is a thing the fixtures manage.",
		Fields: map[string]apidoc.Field{
			"ID":    {Doc: "ID identifies the widget."},
			"Name":  {Doc: "Name is the display name."},
			"Count": {Doc: "Count is how many parts the widget has."},
			"Legacy": {Doc: `Legacy is the pre-rename display name.

Deprecated: Reason: replaced by name. Removal Date: 2027-06-01.`},
			"Note":      {Doc: "Note is an optional remark."},
			"CreatedAt": {Doc: "CreatedAt is when the widget was created."},
			"Tags":      {Doc: "Tags label the widget."},
			"Owner":     {Doc: "Owner is the widget owner."},
			"Updated":   {Doc: "Updated is the last update time."},
			"Secret":    {Doc: "Secret never appears on the wire."},
		},
	}
}

func (Owner) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Owner is who owns a widget.",
		Fields: map[string]apidoc.Field{
			"Login": {Doc: "Login is the owner's login name."},
		},
	}
}

func (Stamp) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Stamp is an epoch-millisecond timestamp held in an integer.",
	}
}

func (CreateWidget) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateWidget is the body for creating a widget.",
		Fields: map[string]apidoc.Field{
			"Name": {Doc: "Name is the display name."},
		},
	}
}

func (Page[_]) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Page wraps a list response.",
		Fields: map[string]apidoc.Field{
			"Data": {Doc: "Data holds the items on this page."},
		},
	}
}

func (Failure) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Failure is the error body.",
		Fields: map[string]apidoc.Field{
			"Message": {Doc: "Message describes the failure."},
		},
	}
}

func (Refusal) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Refusal is the error body of a request the sign-in or permission check refused.",
		Fields: map[string]apidoc.Field{
			"Message": {Doc: "Message describes the refusal."},
			"Reason":  {Doc: "Reason says why the request was refused."},
		},
	}
}

func (BadAnnotations) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "BadAnnotations has broken deprecation and example annotations.",
		Fields: map[string]apidoc.Field{
			"Old": {Doc: `Old is deprecated without saying why.

Deprecated: use New.`},
			"Size": {Doc: "Size has an example that is not a number."},
		},
	}
}

func (Paging) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Paging is the shared pagination block for list endpoints.",
		Fields: map[string]apidoc.Field{
			"PageNumber": {Doc: "PageNumber is the 1-based page to return."},
			"PageSize":   {Doc: "PageSize is how many items each page holds."},
		},
	}
}

func (WidgetQuery) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "WidgetQuery filters the widget list.",
		Fields: map[string]apidoc.Field{
			"Owner":  {Doc: "Owner limits results to one owner."},
			"Expand": {Doc: "Expand names related resources to include."},
			"Colour": {Doc: `Colour limits results by colour.

Deprecated: Reason: replaced by owner. Removal Date: 2027-06-01.`},
		},
	}
}

func (BadQuery) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "BadQuery has broken query parameters.",
		Fields: map[string]apidoc.Field{
			"Untagged": {Doc: "Untagged has no query tag."},
			"Limit":    {Doc: "Limit has a default that is not a number."},
		},
	}
}

func (RuleRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RuleRequest is a request body whose required fields come from validate rules.",
		Fields: map[string]apidoc.Field{
			"Name": {Doc: "Name must be supplied."},
			"Nick": {Doc: "Nick may be left out even though its json tag has no omitempty."},
		},
	}
}

func (HandlerCheckedRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "HandlerCheckedRequest is a request body whose handler checks a field itself.",
		Fields: map[string]apidoc.Field{
			"Model": {Doc: "Model must be supplied and must not be null."},
		},
	}
}

func (RequiredButNullableRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RequiredButNullableRequest has a field that must be sent and may be sent as
null.`,
		Fields: map[string]apidoc.Field{
			"Limit": {Doc: "Limit must be supplied; null means no limit."},
		},
	}
}

func (Optional[_]) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Optional wraps a request field that may be left out.",
	}
}

func (ChangeRule) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ChangeRule is a request body whose rule may be left out.",
		Fields: map[string]apidoc.Field{
			"Rule": {Doc: "Rule replaces the rule; leave it out to keep it."},
		},
	}
}

func (Holder) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Holder links to an owner that may be absent.",
		Fields: map[string]apidoc.Field{
			"Owner": {Doc: "Owner is the owner, or null."},
		},
	}
}

func (Listing) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Listing holds collections the server may send as null.",
		Fields: map[string]apidoc.Field{
			"Labels": {Doc: "Labels maps a key to its label; null when there are none."},
			"Owners": {Doc: "Owners are the listing's owners; null if never set."},
			"Tags":   {Doc: "Tags label the listing."},
		},
	}
}

func (OpenValue) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "OpenValue tags an open value nullable, which it already is.",
		Fields: map[string]apidoc.Field{
			"Shape": {Doc: "Shape is any JSON value; null when it is unknown."},
		},
	}
}

func (Envelope) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Envelope wraps one owner the way a response envelope wraps its payload.",
	}
}

func (PointerEnvelope) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "PointerEnvelope wraps an owner that a route may omit with a 204.",
	}
}

func (Upload) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Upload is a request sent as a multipart form.",
		Fields: map[string]apidoc.Field{
			"File": {Doc: "File is the widget archive."},
			"Note": {Doc: "Note says why the archive is sent."},
		},
	}
}

func (UploadedFile) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UploadedFile is a file received as one part of a form.",
		Fields: map[string]apidoc.Field{
			"Name": {Doc: "Name is the file name the client sent."},
			"Data": {Doc: "Data is the file's content."},
		},
	}
}

func (Chunk) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Chunk is a request whose body is raw bytes.",
		Fields: map[string]apidoc.Field{
			"ID":    {Doc: "ID is the chunk's index."},
			"Bytes": {Doc: "Bytes are the chunk's content."},
		},
	}
}

func (Maybe) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Maybe is a response with a field that is always sent, possibly as null.",
		Fields: map[string]apidoc.Field{
			"Next":  {Doc: "Next is the next page's number; null on the last page."},
			"Later": {Doc: "Later is sent only when set."},
		},
	}
}

func (Checked) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Checked is a request that validates a field in its handler.",
		Fields: map[string]apidoc.Field{
			"Label":  {Doc: "Label is checked by the handler, not a validate rule."},
			"Remark": {Doc: "Remark is optional."},
		},
	}
}
