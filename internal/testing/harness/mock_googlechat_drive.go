// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
)

const (
	GoogleChatDriveFailUpload      = "upload"
	GoogleChatDriveFailShare       = "share"
	GoogleChatDriveFailDelete      = "delete"
	GoogleChatDriveBadFileID       = "bad_file_id"
	GoogleChatDriveFailDeleteError = "delete_error"
)

var GoogleChatDriveFailures = []string{GoogleChatDriveFailUpload, GoogleChatDriveFailShare, GoogleChatDriveFailDelete, GoogleChatDriveBadFileID, GoogleChatDriveFailDeleteError}

const (
	mockDriveUploadPath = "/upload/drive/v3/files"
	mockDriveFilesPath  = "/drive/v3/files/"
	MockDriveFolderID   = "mockdrivefolder01"
)

type MockDriveFile struct {
	ID          string
	Name        string
	MIMEType    string
	Parents     []string
	Content     []byte
	Properties  map[string]string
	Permissions []MockDrivePermission
	Deleted     bool
	Trashed     bool
}

type MockDrivePermission struct {
	Email          string
	Role           string
	Type           string
	ExpirationTime string
	Notified       bool
}

type mockDrive struct {
	mu                  sync.Mutex
	files               []*MockDriveFile
	failures            map[string]int
	nextID              int
	unknownFileRequests []string
}

func (m *MockGoogleChatServer) UnknownDriveFileRequests() []string {
	m.drive.mu.Lock()
	defer m.drive.mu.Unlock()
	return slices.Clone(m.drive.unknownFileRequests)
}

func (m *MockGoogleChatServer) ScriptDriveFailures(names ...string) {
	m.drive.mu.Lock()
	defer m.drive.mu.Unlock()
	if m.drive.failures == nil {
		m.drive.failures = make(map[string]int)
	}
	for _, name := range names {
		m.drive.failures[name]++
	}
}

func (m *MockGoogleChatServer) UnconsumedDriveFailures() []string {
	m.drive.mu.Lock()
	defer m.drive.mu.Unlock()
	var left []string
	for name, n := range m.drive.failures {
		for range n {
			left = append(left, name)
		}
	}
	slices.Sort(left)
	return left
}

func (m *MockGoogleChatServer) DriveFiles() []MockDriveFile {
	m.drive.mu.Lock()
	defer m.drive.mu.Unlock()
	files := make([]MockDriveFile, 0, len(m.drive.files))
	for _, f := range m.drive.files {
		copied := *f
		copied.Permissions = slices.Clone(f.Permissions)
		files = append(files, copied)
	}
	return files
}

func (m *MockGoogleChatServer) DriveURL() string { return m.server.URL + "/drive/v3/" }

func isDriveRequest(path string) bool {
	return strings.HasPrefix(path, mockDriveUploadPath) || strings.HasPrefix(path, mockDriveFilesPath)
}

func (m *MockGoogleChatServer) serveDrive(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == mockDriveUploadPath:
		m.handleDriveUpload(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/permissions"):
		m.handleDriveShare(w, r, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, mockDriveFilesPath), "/permissions"))
	case r.Method == http.MethodDelete:
		m.handleDriveDelete(w, r, strings.TrimPrefix(r.URL.Path, mockDriveFilesPath))
	case r.Method == http.MethodPatch:
		m.handleDriveTrash(w, r, strings.TrimPrefix(r.URL.Path, mockDriveFilesPath))
	default:
		http.Error(w, `{"error":{"code":404,"message":"not found"}}`, http.StatusNotFound)
	}
}

func (d *mockDrive) takeDriveFailure(name string) bool {
	if d.failures[name] == 0 {
		return false
	}
	d.failures[name]--
	return true
}

func requireSharedDrives(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Query().Get("supportsAllDrives") == "true" {
		return true
	}
	writeGoogleError(w, http.StatusNotFound)
	return false
}

var driveFieldsDefault = []string{"kind", "id", "name", "mimeType"}

func driveFileResponse(f *MockDriveFile, fieldsParam string) map[string]any {
	all := map[string]any{"kind": "drive#file", "id": f.ID, "name": f.Name, "mimeType": f.MIMEType}
	out := make(map[string]any)
	pick := func(name string) {
		if v, ok := all[strings.TrimSpace(name)]; ok {
			out[strings.TrimSpace(name)] = v
		}
	}
	if fieldsParam == "" {
		for _, name := range driveFieldsDefault {
			pick(name)
		}
		return out
	}
	for name := range strings.SplitSeq(fieldsParam, ",") {
		pick(name)
	}
	return out
}

func (m *MockGoogleChatServer) handleDriveUpload(w http.ResponseWriter, r *http.Request) {
	m.drive.mu.Lock()
	defer m.drive.mu.Unlock()
	if m.drive.takeDriveFailure(GoogleChatDriveFailUpload) {
		writeGoogleError(w, http.StatusForbidden)
		return
	}
	if !requireSharedDrives(w, r) {
		return
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") || r.URL.Query().Get("uploadType") != "multipart" {
		writeGoogleError(w, http.StatusBadRequest)
		return
	}
	reader := multipart.NewReader(r.Body, params["boundary"])
	metaPart, err := reader.NextPart()
	if err != nil {
		writeGoogleError(w, http.StatusBadRequest)
		return
	}
	metaBytes, _ := io.ReadAll(metaPart)
	var meta struct {
		Name          string            `json:"name"`
		MIMEType      string            `json:"mimeType"`
		Parents       []string          `json:"parents"`
		AppProperties map[string]string `json:"appProperties"`
	}
	if json.Unmarshal(metaBytes, &meta) != nil {
		writeGoogleError(w, http.StatusBadRequest)
		return
	}
	contentPart, err := reader.NextPart()
	if err != nil {
		writeGoogleError(w, http.StatusBadRequest)
		return
	}
	content, _ := io.ReadAll(contentPart)
	m.drive.nextID++
	file := &MockDriveFile{ID: "mockdrivefile" + strconv.Itoa(m.drive.nextID), Name: meta.Name, MIMEType: meta.MIMEType, Parents: meta.Parents, Content: content, Properties: meta.AppProperties}
	m.drive.files = append(m.drive.files, file)
	response := driveFileResponse(file, r.URL.Query().Get("fields"))
	if m.drive.takeDriveFailure(GoogleChatDriveBadFileID) {
		response["id"] = "../not/a/file?id"
	}
	writeJSONBody(w, response)
}

func (m *MockGoogleChatServer) handleDriveShare(w http.ResponseWriter, r *http.Request, fileID string) {
	m.drive.mu.Lock()
	defer m.drive.mu.Unlock()
	if !requireSharedDrives(w, r) {
		return
	}
	var body struct {
		Type           string `json:"type"`
		Role           string `json:"role"`
		EmailAddress   string `json:"emailAddress"`
		ExpirationTime string `json:"expirationTime"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeGoogleError(w, http.StatusBadRequest)
		return
	}
	m.drive.unknownFileRequests = appendIfUnknown(m.drive.unknownFileRequests, m.drive.files, fileID, r.URL.Path)
	if m.drive.takeDriveFailure(GoogleChatDriveFailShare) {
		writeGoogleErrorText(w, http.StatusForbidden, "cannot share "+fileID+" with "+body.EmailAddress)
		return
	}
	for _, f := range m.drive.files {
		if f.ID == fileID {
			f.Permissions = append(f.Permissions, MockDrivePermission{
				Email: body.EmailAddress, Role: body.Role, Type: body.Type, ExpirationTime: body.ExpirationTime,
				Notified: r.URL.Query().Get("sendNotificationEmail") != "false",
			})
			writeJSONBody(w, map[string]string{"id": "perm-" + fileID})
			return
		}
	}
	writeGoogleError(w, http.StatusNotFound)
}

func (m *MockGoogleChatServer) handleDriveDelete(w http.ResponseWriter, r *http.Request, fileID string) {
	m.drive.mu.Lock()
	defer m.drive.mu.Unlock()
	if !requireSharedDrives(w, r) {
		return
	}
	m.drive.unknownFileRequests = appendIfUnknown(m.drive.unknownFileRequests, m.drive.files, fileID, r.URL.Path)
	if m.drive.takeDriveFailure(GoogleChatDriveFailDelete) {
		writeGoogleError(w, http.StatusForbidden)
		return
	}
	if m.drive.takeDriveFailure(GoogleChatDriveFailDeleteError) {
		writeGoogleError(w, http.StatusInternalServerError)
		return
	}
	for _, f := range m.drive.files {
		if f.ID == fileID {
			f.Deleted = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeGoogleError(w, http.StatusNotFound)
}

func (m *MockGoogleChatServer) handleDriveTrash(w http.ResponseWriter, r *http.Request, fileID string) {
	m.drive.mu.Lock()
	defer m.drive.mu.Unlock()
	if !requireSharedDrives(w, r) {
		return
	}
	var body struct {
		Trashed bool `json:"trashed"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || !body.Trashed {
		writeGoogleError(w, http.StatusBadRequest)
		return
	}
	m.drive.unknownFileRequests = appendIfUnknown(m.drive.unknownFileRequests, m.drive.files, fileID, r.URL.Path)
	for _, f := range m.drive.files {
		if f.ID == fileID {
			f.Deleted, f.Trashed = true, true
			writeJSONBody(w, driveFileResponse(f, r.URL.Query().Get("fields")))
			return
		}
	}
	writeGoogleError(w, http.StatusNotFound)
}

func appendIfUnknown(unknown []string, files []*MockDriveFile, fileID, path string) []string {
	if slices.ContainsFunc(files, func(f *MockDriveFile) bool { return f.ID == fileID }) {
		return unknown
	}
	return append(unknown, path)
}
