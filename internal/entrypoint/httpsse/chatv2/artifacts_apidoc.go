// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (ChatArtifact) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ChatArtifact is a file stored for a session or a project.`,
		Fields: map[string]apidoc.Field{
			"ID":           {Doc: `ID is the file's name, which addresses it within its session.`},
			"Type":         {Doc: `Type is always "artifact".`},
			"MimeType":     {Doc: `MimeType is the media type of the latest version. Null when it is unknown.`},
			"Size":         {Doc: `Size is the size of the latest version in bytes.`},
			"UpdatedTime":  {Doc: `UpdatedTime is when the latest version was saved. Null when it is unknown.`},
			"Description":  {Doc: `Description says what the file holds. Null when it has none.`},
			"Schema":       {Doc: `Schema describes the structure of a CSV, JSON or YAML file. Null for any other file.`},
			"URI":          {Doc: `URI refers to the file as artifact://app/user/session/name, without a version. Use it to refer to the file in a message.`},
			"Version":      {Doc: `Version is the number of the latest version. Null when the file has none.`},
			"VersionCount": {Doc: `VersionCount is how many versions the file has. Null when the file has none.`},
			"Source":       {Doc: `Source says what produced the file, such as an agent's tool. Null when it is not recorded.`},
			"Tags":         {Doc: `Tags are labels on the file, such as __user_uploaded.`},
			"SessionID":    {Doc: `SessionID is the UUID of the session that holds the file. Null in a list of one session's files, whose session the path names. Null for a file in a project.`},
			"ProjectID":    {Doc: `ProjectID is the UUID of the project the file belongs to. Null for a file in a session.`},
			"ProjectName":  {Doc: `ProjectName is the name of that project. Null for a file in a session.`},
			"AgentID":      {Doc: `AgentID is the name of the agent of the session that holds the file. Null when the session has none, and for a project's files.`},
		},
	}
}

func (ArtifactVersion) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ArtifactVersion is one saved version of a file.`,
		Fields: map[string]apidoc.Field{
			"ID":      {Doc: `ID is the version number as text.`},
			"Type":    {Doc: `Type is always "artifactVersion".`},
			"Version": {Doc: `Version is the version number, starting at 0.`},
		},
	}
}

func (ArtifactCopyResult) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ArtifactCopyResult says which files a copy reached.`,
		Fields: map[string]apidoc.Field{
			"ID":     {Doc: `ID is the UUID of the session the files were copied into.`},
			"Type":   {Doc: `Type is always "artifactCopy".`},
			"Copied": {Doc: `Copied lists the files that are now in the target session. A user-scoped file, which every session of the user already sees, counts as copied.`},
			"Failed": {Doc: `Failed lists the files that could not be copied.`},
		},
	}
}

func (UploadArtifactRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UploadArtifactRequest uploads a file as the next version of the file with its name.`,
		Fields: map[string]apidoc.Field{
			"File":      {Doc: `File is the file, sent as the file part of a multipart/form-data body.`},
			"SessionID": {Doc: `SessionID is the UUID of the session to store the file in, sent as a part of the body. Leave it out to start a new session, whose id the response returns.`},
			"Filename":  {Doc: `Filename names the file, sent as a part of the body. Leave it out to use the file part's filename. A name that is reserved for internal use or that contains a path fails with 422.`},
			"Metadata":  {Doc: `Metadata is a JSON object of your own fields to keep with the file, sent as a part of the body. Text that is not a JSON object is ignored.`},
		},
	}
}

func (ListAllArtifactsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ListAllArtifactsRequest selects a page of every file the caller can see.`,
		Fields: map[string]apidoc.Field{
			"Page":      {Doc: `Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters.`},
			"Search":    {Doc: `Search keeps the files whose name contains the text, without regard to case.`},
			"ProjectID": {Doc: `ProjectID keeps the files of one project, or of no project when it is __no_project__. A value that is neither a UUID nor __no_project__ fails with 400.`},
		},
	}
}

func (ListSessionArtifactsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ListSessionArtifactsRequest selects a page of the files a session holds.`,
		Fields: map[string]apidoc.Field{
			"ID":         {Doc: `ID is the session's UUID.`},
			"Page":       {Doc: `Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters.`},
			"Visibility": {Doc: `Visibility is visible, the default, or all. The default leaves out the intermediate files an agent keeps while it works, and all includes them with their tags.`},
			"ProjectID":  {Doc: `ProjectID reads the files of a project the caller may view, in place of the session's. A value that is not a UUID fails with 400, and a project the caller may not view or that does not exist fails with 404.`},
		},
	}
}

func (ArtifactRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ArtifactRequest addresses a file in a session the caller owns by its name.`,
		Fields: map[string]apidoc.Field{
			"SessionID": {Doc: `SessionID is the session's UUID.`},
			"Filename":  {Doc: `Filename is the file's name, as its id.`},
		},
	}
}

func (ListArtifactVersionsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ListArtifactVersionsRequest selects a page of the versions of a file.`,
		Fields: map[string]apidoc.Field{
			"ProjectID": {Doc: `ProjectID reads the file from a project the caller may view, in place of the session's. A value that is not a UUID fails with 400, and a project the caller may not view or that does not exist fails with 404.`},
			"Page":      {Doc: `Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters.`},
		},
	}
}

func (DownloadArtifactRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `DownloadArtifactRequest addresses a file in a session by its name, for a download.`,
		Fields: map[string]apidoc.Field{
			"ProjectID": {Doc: `ProjectID reads the file from a project the caller may view, in place of the session's. A value that is not a UUID fails with 400, and a project the caller may not view or that does not exist fails with 404.`},
			"MaxBytes":  {Doc: `MaxBytes sends only the first bytes of the file, from 1 to 1048576, for a preview. A value that is not a number fails with 400, and one outside that range fails with 422. A cut file carries the header X-Truncated: true and its full size in X-Original-Size.`},
		},
	}
}

func (DownloadArtifactVersionRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `DownloadArtifactVersionRequest addresses one version of a file in a session.`,
		Fields: map[string]apidoc.Field{
			"Version": {Doc: `Version is the version number, or latest. A value that is neither a number nor latest fails with 400, and a negative number fails with 422.`},
		},
	}
}

func (DownloadArtifactByURIRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `DownloadArtifactByURIRequest names a file by its artifact:// uri.`,
		Fields: map[string]apidoc.Field{
			"URI":      {Doc: `URI is the file's artifact:// uri with ?version=N, which is required, as the uri of an upload or a list gives it plus the version. A uri that does not start with artifact://, has no version or has a version that is not a number fails with 400. The caller must own the file or be allowed to view its project, or the request fails with 404.`},
			"MaxBytes": {Doc: `MaxBytes sends only the first bytes of the file, from 1 to 1048576, for a preview. A value that is not a number fails with 400, and one outside that range fails with 422.`},
		},
	}
}

func (CopyArtifactsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CopyArtifactsRequest copies files from one of the caller's sessions into another.`,
		Fields: map[string]apidoc.Field{
			"ID":              {Doc: `ID is the UUID of the session to copy into. A session that does not exist yet is created.`},
			"SourceSessionID": {Doc: `SourceSessionID is the UUID of the session to copy from, which the caller must own. A value that is not a UUID fails with 422.`},
			"Filenames":       {Doc: `Filenames are the files to copy. Only the latest version of each is copied.`},
		},
	}
}

func (ScheduledArtifactRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ScheduledArtifactRequest addresses a file a scheduled task produced.`,
		Fields: map[string]apidoc.Field{
			"SessionID": {Doc: `SessionID is the UUID of the session of the task's run.`},
			"Filename":  {Doc: `Filename is the file's name.`},
			"Download":  {Doc: `Download is true to ask for an attachment in every case, or false, the default, to send a file a browser can show safely, such as an image or a PDF, inline. Any other value fails with 400.`},
		},
	}
}
