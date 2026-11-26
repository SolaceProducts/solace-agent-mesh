// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (IDRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "IDRequest addresses one resource by its path id.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the resource's UUID."},
		},
	}
}

func (UploadedFile) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UploadedFile is a file sent as one part of a multipart/form-data body. The
part is required.`,
		Fields: map[string]apidoc.Field{
			"Name":        {Doc: "Name is the file name the client sent with the part."},
			"ContentType": {Doc: "ContentType is the media type the client sent with the part."},
		},
	}
}
