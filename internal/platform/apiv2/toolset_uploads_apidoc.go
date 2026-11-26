// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (ToolsetUpload) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ToolsetUpload is an upload of a toolset's bundle in chunks, for a bundle too
large for one request. Create it, PUT each chunk, then complete it. The
upload is gone after it is completed or expires.`,
		Fields: map[string]apidoc.Field{
			"ID":        {Doc: "ID is the upload's UUID."},
			"Type":      {Doc: "Type is always \"toolsetUpload\"."},
			"ToolsetID": {Doc: "ToolsetID is the UUID of the toolset whose bundle the upload replaces."},
			"Filename":  {Doc: "Filename is the name the bundle is stored under. It ends in .zip."},
			"TotalSize": {Doc: "TotalSize is the size in bytes of the whole bundle, as declared when the upload was created."},
			"ChunkSize": {Doc: `ChunkSize is the size in bytes of every chunk but the last. Split the bundle at this size:
chunk 0 is its first chunkSize bytes, chunk 1 the next, and so on.`},
			"ReceivedBytes": {Doc: "ReceivedBytes is the total size in bytes of the chunks received so far."},
			"ReceivedChunks": {Doc: `ReceivedChunks lists the indexes of the chunks received so far, in ascending order. Empty
before the first chunk arrives.`},
			"CreatedTime": {Doc: "CreatedTime is when the upload was created."},
			"ExpiresTime": {Doc: "ExpiresTime is when the upload is discarded if it has not been completed."},
		},
	}
}

func (ToolsetBuildTarget) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ToolsetBuildTarget is the platform and Python version to build a toolset's
bundle for, so that it runs in the tool runtime.`,
		Fields: map[string]apidoc.Field{
			"OS":   {Doc: "OS is the operating system the tool runtime runs on, such as linux."},
			"Arch": {Doc: "Arch is the processor architecture the tool runtime runs on, such as amd64 or arm64."},
			"Source": {Doc: `Source is where os and arch come from: str_init when a tool runtime reported its platform, or
fallback when none has yet. With fallback, os and arch are Agent Mesh defaults, which may not
match your deployment.`},
			"PythonVersion": {Doc: `PythonVersion is the Python version, as major.minor, the tool runtime runs Python tools with.
Null when no tool runtime has reported it.`},
		},
	}
}

func (CreateToolsetUploadRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateToolsetUploadRequest starts a chunked upload of a toolset's bundle.",
		Fields: map[string]apidoc.Field{
			"ToolsetID": {Doc: "ToolsetID is the UUID of the toolset to upload the bundle for."},
			"Filename":  {Doc: "Filename is the name to store the bundle under. It must end in .zip."},
			"TotalSize": {Doc: `TotalSize is the size in bytes of the whole bundle, from 1 to the deployment's maximum upload
size, 100 MB by default.`},
		},
	}
}

func (ReplaceToolsetUploadChunkRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ReplaceToolsetUploadChunkRequest sends one chunk of an upload.",
		Fields: map[string]apidoc.Field{
			"ToolsetUploadID": {Doc: "ToolsetUploadID is the UUID of the upload the chunk belongs to."},
			"ID":              {Doc: "ID is the chunk's index, counting from 0. Sending an index again replaces that chunk."},
			"Chunk": {Doc: `Chunk is the chunk's bytes as the raw request body: exactly chunkSize bytes for every chunk
but the last, which holds the rest.`},
		},
	}
}
