// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import (
	"net/http"
	"net/url"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
)

const GateArtifactOwner apicontract.Gate = "artifactOwner"

const GateArtifactReader apicontract.Gate = "artifactReader"

const GateScheduledArtifact apicontract.Gate = "scheduledArtifact"

var artifactsTag = apicontract.Tag{Name: "Chat Artifacts", Group: tagGroup, Text: "Upload files, refer to them in messages, and list and download the files a session holds."}

const artifactDownloadFailedMessage = "Unable to download the artifact"

const artifactReferenceText = "A file is referred to by an artifact:// uri, which every artifact in a response carries as uri. " +
	"To refer to a file in a message, put a part {\"kind\": \"file\", \"file\": {\"uri\": \"<uri>\"}} in the message's parts. " +
	"Send a uri without a version to mean the latest version at the time of the send. "

const artifactFetchText = "A browser cannot send the Authorization header from a link, so fetch the file with the Bearer header and turn the response into a blob (fetch, then URL.createObjectURL) instead of putting the URL in an <a href>. "

const artifactDownloadText = "The file is sent as an attachment. " + artifactFetchText

const scheduledArtifactDownloadText = "A file a browser can show safely, such as an image or a PDF, is sent inline, and any other file as an attachment; send download=true to get an attachment in every case. " + artifactFetchText

const artifactUploadLimitText = "The gateway fails a body larger than its upload limit (gateway_max_upload_size_bytes, 50 MB by default) with 413 before this route runs, and the route fails a file larger than the artifact content limit (gateway_artifact_content_limit_bytes, 10 MB by default) with 413. "

type ChatArtifact struct {
	ID string `json:"id" example:"report.pdf"`

	Type string `json:"type" example:"artifact"`

	MimeType *string `json:"mimeType" example:"application/pdf"`

	Size int64 `json:"size" example:"48213"`

	UpdatedTime apicontract.Instant `json:"updatedTime"`

	Description *string `json:"description" example:"Quarterly revenue report"`

	Schema map[string]any `json:"schema" example:"{\"type\":\"object\"}"`

	URI *string `json:"uri" example:"artifact://sam/jane.doe@example.com/0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c/report.pdf"`

	Version *int `json:"version" example:"3"`

	VersionCount *int `json:"versionCount" example:"3"`

	Source *string `json:"source" example:"user_upload"`

	Tags []string `json:"tags" example:"[\"__user_uploaded\"]"`

	SessionID *string `json:"sessionId" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`

	ProjectID *string `json:"projectId" example:"0190f5e7-5e6f-7b8c-9d0e-1f2a3b4c5d6e"`

	ProjectName *string `json:"projectName" example:"Support runbooks"`

	AgentID *string `json:"agentId" example:"Support"`
}

func (a ChatArtifact) Location() string {
	session := ""
	if a.SessionID != nil {
		session = *a.SessionID
	}
	return Prefix + "/sessions/" + url.PathEscape(session) + "/artifacts/" + url.PathEscape(a.ID)
}

type ArtifactVersion struct {
	ID string `json:"id" example:"3"`

	Type string `json:"type" example:"artifactVersion"`

	Version int `json:"version" example:"3"`
}

type ArtifactCopyResult struct {
	ID string `json:"id" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`

	Type string `json:"type" example:"artifactCopy"`

	Copied []string `json:"copied" example:"[\"report.pdf\"]"`

	Failed []string `json:"failed" example:"[]"`
}

type UploadArtifactRequest struct {
	File apicontract.UploadedFile `form:"file" json:"-"`

	SessionID string `form:"sessionId" json:"-" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`

	Filename string `form:"filename" json:"-" example:"report.pdf"`

	Metadata string `form:"metadata" json:"-" example:"{\"purpose\":\"q3-review\"}"`
}

type ListAllArtifactsRequest struct {
	apicontract.StrictRequest

	Page apicontract.Page `json:"-"`

	Search string `query:"search" json:"-" example:"report"`

	ProjectID string `query:"projectId" json:"-" example:"0190f5e7-5e6f-7b8c-9d0e-1f2a3b4c5d6e"`
}

type ListSessionArtifactsRequest struct {
	apicontract.StrictRequest

	ID string `path:"id" json:"-"`

	Page apicontract.Page `json:"-"`

	Visibility string `query:"visibility" json:"-" example:"visible"`

	ProjectID string `query:"projectId" json:"-" example:"0190f5e7-5e6f-7b8c-9d0e-1f2a3b4c5d6e"`
}

type ArtifactRequest struct {
	apicontract.StrictRequest

	SessionID string `path:"sessionId" json:"-"`

	Filename string `path:"filename,text" json:"-" example:"report.pdf"`
}

type ListArtifactVersionsRequest struct {
	ArtifactRequest

	ProjectID string `query:"projectId" json:"-" example:"0190f5e7-5e6f-7b8c-9d0e-1f2a3b4c5d6e"`

	Page apicontract.Page `json:"-"`
}

type DownloadArtifactRequest struct {
	ArtifactRequest

	ProjectID string `query:"projectId" json:"-" example:"0190f5e7-5e6f-7b8c-9d0e-1f2a3b4c5d6e"`

	MaxBytes string `query:"maxBytes" json:"-" example:"4096"`
}

type DownloadArtifactVersionRequest struct {
	DownloadArtifactRequest

	Version string `path:"version,text" json:"-" example:"2"`
}

type DownloadArtifactByURIRequest struct {
	apicontract.StrictRequest

	URI string `query:"uri" json:"-" example:"artifact://sam/jane.doe@example.com/0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c/report.pdf?version=2"`

	MaxBytes string `query:"maxBytes" json:"-" example:"4096"`
}

type CopyArtifactsRequest struct {
	ID string `path:"id" json:"-"`

	SourceSessionID string `json:"sourceSessionId" validate:"required" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`

	Filenames []string `json:"filenames" validate:"required,min=1" example:"[\"report.pdf\"]"`
}

type ScheduledArtifactRequest struct {
	apicontract.StrictRequest

	SessionID string `path:"sessionId" json:"-"`

	Filename string `path:"filename,text" json:"-" example:"digest.pdf"`

	Download string `query:"download" json:"-" example:"true"`
}

func ArtifactNotFound() error {
	return &apicontract.CallerError{Status: http.StatusNotFound, Message: "artifact not found"}
}

func ArtifactRoutes(deps Deps) []apicontract.Route {
	d := deps
	return []apicontract.Route{
		{Method: http.MethodPost, Path: "/artifacts", Gate: GateArtifactOwner, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "uploadChatArtifact", Summary: "Create an artifact",
				Text: "Stores the file as the next version of the file with its name and returns it. " + artifactReferenceText + artifactUploadLimitText +
					"A CSV, JSON or YAML file also gets a schema. The Content-Type of the request must be multipart/form-data.",
				Errors: []int{http.StatusRequestEntityTooLarge}, Tag: artifactsTag},
			Handler: apicontract.Handle(callIf(d.UploadArtifact), "Unable to upload the artifact")},
		{Method: http.MethodGet, Path: "/artifacts", Gate: GateArtifactReader, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "listChatArtifacts", Summary: "Get a list of artifacts",
				Text: "Returns one page of every file the caller can see: those in their own sessions and those in projects they may view, by name.",
				Tag:  artifactsTag},
			Handler: apicontract.Handle(callIf(d.ListAllArtifacts), "Unable to list artifacts")},
		{Method: http.MethodGet, Path: "/artifacts/byUri", Gate: GateArtifactReader, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "downloadChatArtifactByUri", Summary: "Get an artifact by its uri",
				Text: "Sends the version of the file the uri names. " + artifactDownloadText + "A file of another user's that the caller may not view answers 404.",
				Tag:  artifactsTag},
			Handler: apicontract.Handle(callIf(d.DownloadArtifactByURI), artifactDownloadFailedMessage)},
		{Method: http.MethodGet, Path: "/sessions/{id}/artifacts", Gate: GateArtifactReader, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "listChatSessionArtifacts", Summary: "Get a list of the artifacts of a session",
				Text: "Returns one page of the files a session holds, by name. A session that belongs to another user holds no files for the caller, so its list is empty.",
				Tag:  artifactsTag},
			Handler: apicontract.Handle(callIf(d.ListSessionArtifacts), "Unable to list the session's artifacts")},
		{Method: http.MethodGet, Path: "/sessions/{sessionId}/artifacts/{filename}", Gate: GateArtifactReader, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "downloadChatArtifact", Summary: "Get an artifact",
				Text: "Sends the latest version of the file. " + artifactDownloadText, Tag: artifactsTag},
			Handler: apicontract.Handle(callIf(d.DownloadArtifact), artifactDownloadFailedMessage)},
		{Method: http.MethodGet, Path: "/sessions/{sessionId}/artifacts/{filename}/versions", Gate: GateArtifactReader, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "listChatArtifactVersions", Summary: "Get a list of the versions of an artifact",
				Text: "Returns the version numbers of the file, oldest first, as one page.", Tag: artifactsTag},
			Handler: apicontract.Handle(callIf(d.ListArtifactVersions), "Unable to list the artifact's versions")},
		{Method: http.MethodGet, Path: "/sessions/{sessionId}/artifacts/{filename}/versions/{version}", Gate: GateArtifactReader, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "downloadChatArtifactVersion", Summary: "Get a version of an artifact",
				Text: "Sends one version of the file, or the latest when the version is latest. " + artifactDownloadText, Tag: artifactsTag},
			Handler: apicontract.Handle(callIf(d.DownloadArtifactVersion), artifactDownloadFailedMessage)},
		{Method: http.MethodDelete, Path: "/sessions/{sessionId}/artifacts/{filename}", Gate: GateArtifactOwner, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "deleteChatArtifact", Summary: "Delete an artifact",
				Text: "Deletes every version of the file from a session the caller owns.", Tag: artifactsTag},
			Handler: apicontract.Handle(callIf(d.DeleteArtifact), "Unable to delete the artifact")},
		{Method: http.MethodPut, Path: "/sessions/{id}/artifacts/copy", Gate: GateArtifactOwner, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "copyChatArtifacts", Summary: "Update a session to copy artifacts into it",
				Text: "Copies the latest version of each named file from another of the caller's sessions. A file that cannot be copied is listed in failed and does not fail the request. A source session that is not the caller's answers 404.",
				Tag:  artifactsTag},
			Handler: apicontract.Handle(callIf(d.CopyArtifacts), "Unable to copy the artifacts")},
	}
}

func ScheduledArtifactRoutes(deps Deps) []apicontract.Route {
	return []apicontract.Route{
		{Method: http.MethodGet, Path: "/artifacts/scheduled/{sessionId}/{filename}", Gate: GateScheduledArtifact, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "downloadChatScheduledArtifact", Summary: "Get an artifact a scheduled task produced",
				Text: "Sends a file that a run of one of the caller's scheduled tasks produced. " + scheduledArtifactDownloadText + "A task that belongs to another user answers 404.",
				Tag:  artifactsTag},
			Handler: apicontract.Handle(callIf(deps.DownloadScheduledArtifact), artifactDownloadFailedMessage)},
	}
}
