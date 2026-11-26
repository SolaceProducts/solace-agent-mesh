// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/googleapi"
)

func TestClassifyGoogleError(t *testing.T) {
	transport := errors.New("connection reset")
	throttled := &googleapi.Error{Code: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"7"}}}

	if err := classifyGoogleError(nil); err != nil {
		t.Errorf("nil error classified as %v", err)
	}
	if err := classifyGoogleError(transport); err != transport {
		t.Errorf("transport error classified as %v, want it unchanged (transient)", err)
	}

	limited, ok := errors.AsType[*rateLimitedError](classifyGoogleError(throttled))
	if !ok || limited.retryAfter != 7*time.Second {
		t.Errorf("429 classified as %+v, want rateLimitedError honoring Retry-After 7s", limited)
	}
	if limited, _ := errors.AsType[*rateLimitedError](classifyGoogleError(&googleapi.Error{Code: http.StatusTooManyRequests})); limited == nil || limited.retryAfter != 0 {
		t.Errorf("429 without Retry-After classified as %+v, want rateLimitedError with no hint", limited)
	}
	if err := classifyGoogleError(&googleapi.Error{Code: http.StatusNotFound}); !errors.Is(err, errMessageGone) {
		t.Errorf("404 classified as %v, want errMessageGone", err)
	}
	for _, code := range []int{http.StatusBadRequest, http.StatusForbidden} {
		if _, ok := errors.AsType[*permanentError](classifyGoogleError(&googleapi.Error{Code: code})); !ok {
			t.Errorf("%d not classified as permanent", code)
		}
	}
	for _, code := range []int{http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		err := classifyGoogleError(&googleapi.Error{Code: code})
		if _, permanent := errors.AsType[*permanentError](err); permanent || errors.Is(err, errMessageGone) {
			t.Errorf("%d classified as %v, want transient", code, err)
		}
	}
}

func TestRetryAfterHintIgnoresUnusableValues(t *testing.T) {
	for _, value := range []string{"", "0", "-3", "Wed, 21 Oct 2026 07:28:00 GMT"} {
		if got := retryAfterHint(http.Header{"Retry-After": {value}}); got != 0 {
			t.Errorf("Retry-After %q gave %v, want 0", value, got)
		}
	}
	if got := retryAfterHint(nil); got != 0 {
		t.Errorf("nil header gave %v, want 0", got)
	}
}

func TestDownloadAttachment_EnforcesTheSizeCap(t *testing.T) {
	const capBytes = 16
	for name, tc := range map[string]struct {
		serve    func(w http.ResponseWriter)
		wantData string
		wantErr  error
	}{
		"within the cap": {
			serve:    func(w http.ResponseWriter) { _, _ = io.WriteString(w, "small file") },
			wantData: "small file",
		},
		"declared over the cap": {
			serve: func(w http.ResponseWriter) {
				w.Header().Set("Content-Length", strconv.Itoa(capBytes*64))
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, "tiny")
			},
			wantErr: errAttachmentTooLarge,
		},
		"streamed past the cap": {
			serve: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				_, _ = io.WriteString(w, strings.Repeat("x", capBytes+1))
			},
			wantErr: errAttachmentTooLarge,
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { tc.serve(w) }))
			t.Cleanup(srv.Close)
			api, err := newRestChatAPI(t.Context(), &Config{ChatAPIBaseURL: srv.URL + "/", TestSkipGoogleAuth: true})
			if err != nil {
				t.Fatal(err)
			}
			data, err := api.DownloadAttachment(t.Context(), "spaces/S/messages/M/attachments/A", capBytes)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("DownloadAttachment = (%d bytes, %v), want %v", len(data), err, tc.wantErr)
				}
				return
			}
			if err != nil || string(data) != tc.wantData {
				t.Errorf("DownloadAttachment = (%q, %v), want %q", data, err, tc.wantData)
			}
		})
	}
}

func TestUpdateMessage_NeverSendsAnEmptyBody(t *testing.T) {
	for name, tc := range map[string]struct {
		content     messageContent
		replace     bool
		wantMask    string
		wantNoCards bool
	}{
		"text only":              {content: messageContent{Text: "answer"}, wantMask: updateMaskText},
		"text and cards":         {content: messageContent{Text: "answer", Cards: cancelControls("t", "t")}, replace: true, wantMask: updateMaskTextCards},
		"cards only":             {content: messageContent{Cards: cancelControls("t", "t")}, replace: true, wantMask: updateMaskCards},
		"clearing cards":         {replace: true, wantMask: updateMaskCards, wantNoCards: true},
		"clearing cards by text": {content: messageContent{Text: "answer"}, replace: true, wantMask: updateMaskTextCards, wantNoCards: true},
	} {
		t.Run(name, func(t *testing.T) {
			var mask string
			var body struct {
				Text    string            `json:"text"`
				CardsV2 []json.RawMessage `json:"cardsV2"`
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mask = r.URL.Query().Get("updateMask")
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request body: %v", err)
				}
				_, _ = io.WriteString(w, "{}")
			}))
			t.Cleanup(srv.Close)
			api, err := newRestChatAPI(t.Context(), &Config{ChatAPIBaseURL: srv.URL + "/", TestSkipGoogleAuth: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := api.UpdateMessage(t.Context(), "spaces/S/messages/M", tc.content, tc.replace); err != nil {
				t.Fatalf("UpdateMessage: %v", err)
			}
			if mask != tc.wantMask {
				t.Errorf("update mask = %q, want %q", mask, tc.wantMask)
			}
			if body.Text == "" && len(body.CardsV2) == 0 {
				t.Error("the update body has neither text nor cards, which Chat rejects as an empty message")
			}
			if tc.wantNoCards && len(body.CardsV2) != 0 {
				t.Errorf("body cards = %d, want none", len(body.CardsV2))
			}
		})
	}
}

func TestGoogleClientOptionsRejectsUnusableKey(t *testing.T) {
	if _, err := googleClientOptions(t.Context(), &Config{ServiceAccountKey: `{"type":"authorized_user"}`}, "https://chat.googleapis.com/", chatBotScope); err == nil {
		t.Fatal("a key that is not a service account key was accepted")
	}
}
