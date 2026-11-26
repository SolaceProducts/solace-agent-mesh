// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
)

const widgetID = "019f9b46-77bc-76ba-a494-017487b5f5c4"

type widget struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Label string `json:"label,omitempty"`
}

type getWidgetRequest struct {
	ID string `path:"id" json:"-"`
}

type getWidgetTypeRequest struct {
	ID string `path:"id,key" json:"-"`
}

type listWidgetsRequest struct {
	Page    Page      `json:"-"`
	Sort    SortOrder `sort:"label,createdTime" json:"-"`
	Label   string    `query:"label" json:"-"`
	Surface string    `header:"X-Sam-Client-Surface" json:"-"`
}

type widgetFileRequest struct {
	ID       string `path:"id" json:"-"`
	Filename string `path:"filename,text" json:"-"`
}

type updateWidgetRequest struct {
	ID    string        `path:"id" json:"-"`
	Label Patch[string] `json:"label"`
}

func widgetMux(got *any) *http.ServeMux {
	mux := http.NewServeMux()
	for pattern, h := range map[string]Handler{
		"GET /widgets": Handle(func(_ context.Context, req listWidgetsRequest) (List[widget], error) {
			*got = req
			return List[widget]{Items: []widget{{ID: "w6", Type: "widget"}}, Total: 6}, nil
		}, "Unable to list widgets"),
		"GET /widgets/{id}": Handle(func(_ context.Context, req getWidgetRequest) (widget, error) {
			*got = req
			if req.ID != widgetID {
				return widget{}, &apierr.NotFoundError{Resource: "widget", ID: req.ID}
			}
			return widget{ID: req.ID, Type: "widget"}, nil
		}, "Unable to get widget"),
		"GET /widgetTypes/{id}": Handle(func(_ context.Context, req getWidgetTypeRequest) (widget, error) {
			*got = req
			return widget{ID: req.ID, Type: "widgetType"}, nil
		}, "Unable to get widget type"),
		"POST /widgets": Handle(func(_ context.Context, req createWidgetRequest) (widget, error) {
			*got = req
			return widget{ID: widgetID, Type: "widget", Label: req.Name}, nil
		}, "Unable to create widget"),
		"PATCH /widgets/{id}": Handle(func(_ context.Context, req updateWidgetRequest) (widget, error) {
			*got = req
			return widget{ID: req.ID, Type: "widget", Label: req.Label.Value}, nil
		}, "Unable to update widget"),
		"DELETE /widgets/{id}": Handle(func(_ context.Context, req getWidgetRequest) (struct{}, error) {
			*got = req
			return struct{}{}, nil
		}, "Unable to delete widget"),
		"PUT /widgets/{id}/refresh": Handle(func(_ context.Context, req getWidgetRequest) (struct{}, error) {
			*got = req
			return struct{}{}, nil
		}, "Unable to refresh widget"),
		"PUT /widgets/{id}/rebuild": Handle(func(_ context.Context, req getWidgetRequest) (Accepted, error) {
			*got = req
			return Accepted{
				Operation: Operation{ID: widgetID, Type: "operation", ResourceID: &req.ID, OperationType: "rebuild", Status: OperationPending},
				Location:  "/widgets/" + req.ID + "/operations/" + widgetID,
			}, nil
		}, "Unable to rebuild widget"),
		"GET /widgets/{id}/files/{filename...}": Handle(func(_ context.Context, req widgetFileRequest) (Download, error) {
			*got = req
			return Download{Content: []byte("<p>hi</p>"), MimeType: "text/html", Filename: req.Filename}, nil
		}, "Unable to get widget file"),
		"PUT /widgets/{id}/publish": Handle(func(_ context.Context, req getWidgetRequest) (widget, error) {
			*got = req
			return widget{}, &LocatedError{
				Err:      &CallerError{Status: http.StatusConflict, Message: "The widget is already published"},
				Location: "/widgets/" + req.ID,
			}
		}, "Unable to publish widget"),
	} {
		mux.HandleFunc(pattern, h.serve)
	}
	return mux
}

func serveWidget(mux *http.ServeMux, method, target, body string) *httptest.ResponseRecorder {
	return serveWidgetRequest(mux, httptest.NewRequest(method, target, strings.NewReader(body)))
}

func serveWidgetRequest(mux *http.ServeMux, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

func TestHandle_BindsRequestAndWritesResponse(t *testing.T) {
	const one = `{"data":{"id":"` + widgetID + `","type":"widget"}}` + "\n"
	for _, tc := range []struct {
		name       string
		method     string
		target     string
		body       string
		wantReq    any
		wantStatus int
		wantBody   string
	}{
		{"list reads the page, sort and query", http.MethodGet, "/widgets?pageNumber=2&pageSize=5&sort=label:desc&label=a", "",
			listWidgetsRequest{Page: Page{Number: 2, Size: 5}, Sort: SortOrder{Field: "label", Desc: true}, Label: "a"}, http.StatusOK,
			`{"data":[{"id":"w6","type":"widget"}],"meta":{"pagination":{"pageNumber":2,"count":6,"pageSize":5,"nextPage":null,"totalPages":2}}}` + "\n"},
		{"list defaults the page", http.MethodGet, "/widgets", "",
			listWidgetsRequest{Page: Page{Number: 1, Size: DefaultPageSize}}, http.StatusOK,
			`{"data":[{"id":"w6","type":"widget"}],"meta":{"pagination":{"pageNumber":1,"count":6,"pageSize":20,"nextPage":null,"totalPages":1}}}` + "\n"},
		{"get reads the path", http.MethodGet, "/widgets/" + widgetID, "",
			getWidgetRequest{ID: widgetID}, http.StatusOK, one},
		{"get reads a key from the path", http.MethodGet, "/widgetTypes/event_mesh", "",
			getWidgetTypeRequest{ID: "event_mesh"}, http.StatusOK,
			`{"data":{"id":"event_mesh","type":"widgetType"}}` + "\n"},
		{"create answers 201", http.MethodPost, "/widgets", `{"name":"a","description":"b"}`,
			createWidgetRequest{Name: "a", Description: "b"}, http.StatusCreated,
			`{"data":{"id":"` + widgetID + `","type":"widget","label":"a"}}` + "\n"},
		{"update reads the path and body", http.MethodPatch, "/widgets/" + widgetID, `{"label":null}`,
			updateWidgetRequest{ID: widgetID, Label: Patch[string]{Set: true, Null: true}}, http.StatusOK, one},
		{"delete answers 204", http.MethodDelete, "/widgets/" + widgetID, "",
			getWidgetRequest{ID: widgetID}, http.StatusNoContent, ""},
		{"any method returning nothing answers 204", http.MethodPut, "/widgets/" + widgetID + "/refresh", "",
			getWidgetRequest{ID: widgetID}, http.StatusNoContent, ""},
		{"accepted work answers 202 with its operation", http.MethodPut, "/widgets/" + widgetID + "/rebuild", "",
			getWidgetRequest{ID: widgetID}, http.StatusAccepted,
			`{"data":{"id":"` + widgetID + `","type":"operation","resourceId":"` + widgetID + `","operationType":"rebuild","createdBy":"","createdTime":null,"completedTime":null,"status":"pending","error":null}}` + "\n"},
		{"download reads a text wildcard and sends the file as stored", http.MethodGet, "/widgets/" + widgetID + "/files/docs/a%20b.html", "",
			widgetFileRequest{ID: widgetID, Filename: "docs/a b.html"}, http.StatusOK, "<p>hi</p>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got any
			rec := serveWidget(widgetMux(&got), tc.method, tc.target, tc.body)

			if rec.Code != tc.wantStatus || rec.Body.String() != tc.wantBody {
				t.Errorf("got %d %s\nwant %d %s", rec.Code, rec.Body.String(), tc.wantStatus, tc.wantBody)
			}
			if !reflect.DeepEqual(got, tc.wantReq) {
				t.Errorf("handler got %+v, want %+v", got, tc.wantReq)
			}
		})
	}
}

func TestHandle_Location(t *testing.T) {
	for _, tc := range []struct {
		method, target, body, want string
	}{
		{http.MethodPost, "/widgets", `{"name":"a","description":"b"}`, "/widgets/" + widgetID},
		{http.MethodGet, "/widgets/" + widgetID, "", ""},
		{http.MethodPatch, "/widgets/" + widgetID, `{"label":"a"}`, ""},
		{http.MethodDelete, "/widgets/" + widgetID, "", ""},
	} {
		var got any
		rec := serveWidget(widgetMux(&got), tc.method, tc.target, tc.body)
		if location := rec.Header().Get("Location"); location != tc.want {
			t.Errorf("%s %s: Location = %q, want %q", tc.method, tc.target, location, tc.want)
		}
	}
}

func TestHandle_AcceptedAndDownloadHeaders(t *testing.T) {
	var got any
	mux := widgetMux(&got)

	rec := serveWidget(mux, http.MethodPut, "/widgets/"+widgetID+"/rebuild", "")
	if want := "/widgets/" + widgetID + "/operations/" + widgetID; rec.Header().Get("Location") != want {
		t.Errorf("accepted Location = %q, want %q", rec.Header().Get("Location"), want)
	}

	rec = serveWidget(mux, http.MethodGet, "/widgets/"+widgetID+"/files/r\u00e9sum\u00e9.html", "")
	for header, want := range map[string]string{
		"Content-Type":           "text/html",
		"X-Content-Type-Options": "nosniff",
		"Content-Disposition":    "attachment; filename*=utf-8''r%C3%A9sum%C3%A9.html",
	} {
		if v := rec.Header().Get(header); v != want {
			t.Errorf("download %s = %q, want %q", header, v, want)
		}
	}
}

func TestHandle_LocationIDBehindNilPointer(t *testing.T) {
	type identified struct {
		ID string `json:"id"`
	}
	type created struct {
		*identified
		Type string `json:"type"`
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /widgets", Handle(func(context.Context, struct{}) (created, error) {
		return created{Type: "widget"}, nil
	}, "Unable to create widget").serve)

	rec := serveWidget(mux, http.MethodPost, "/widgets", "{}")
	if rec.Code != http.StatusCreated || rec.Header().Get("Location") != "" {
		t.Errorf("status %d Location %q, want 201 and none", rec.Code, rec.Header().Get("Location"))
	}
}

func TestHandle_LocationFromKeyField(t *testing.T) {
	type pinned struct {
		AgentName string `json:"agentName" locationKey:""`
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /widgets/{id}/agents", Handle(func(context.Context, getWidgetRequest) (pinned, error) {
		return pinned{AgentName: "Web Agent"}, nil
	}, "Unable to pin agent").serve)

	rec := serveWidget(mux, http.MethodPost, "/widgets/"+widgetID+"/agents", "{}")
	if want := "/widgets/" + widgetID + "/agents/Web%20Agent"; rec.Code != http.StatusCreated || rec.Header().Get("Location") != want {
		t.Errorf("status %d Location %q, want 201 and %q", rec.Code, rec.Header().Get("Location"), want)
	}
}

func TestHandle_BindsHeader(t *testing.T) {
	var got any
	r := httptest.NewRequest(http.MethodGet, "/widgets", nil)
	r.Header.Set("X-Sam-Client-Surface", "cli")
	serveWidgetRequest(widgetMux(&got), r)

	if req, _ := got.(listWidgetsRequest); req.Surface != "cli" {
		t.Errorf("handler got %+v, want Surface cli", got)
	}
}

func TestHandle_Errors(t *testing.T) {
	for _, tc := range []struct {
		name        string
		method      string
		target      string
		body        string
		wantStatus  int
		wantMessage string
		wantFields  []string
		wantCalled  bool
	}{
		{"path id is not a UUID", http.MethodGet, "/widgets/w1", "", http.StatusBadRequest, MsgInvalidPathParam, []string{"id"}, false},
		{"path key is not lowercase", http.MethodGet, "/widgetTypes/Event_Mesh", "", http.StatusBadRequest, MsgInvalidPathParam, []string{"id"}, false},
		{"path key is not snake_case", http.MethodGet, "/widgetTypes/event-mesh", "", http.StatusBadRequest, MsgInvalidPathParam, []string{"id"}, false},
		{"path text is empty", http.MethodGet, "/widgets/" + widgetID + "/files/", "", http.StatusBadRequest, MsgInvalidPathParam, []string{"filename"}, false},
		{"page size out of range", http.MethodGet, "/widgets?pageSize=500", "", http.StatusUnprocessableEntity, MsgInvalidQueryParam, []string{"pageSize"}, false},
		{"sort is malformed", http.MethodGet, "/widgets?sort=label:up", "", http.StatusBadRequest, MsgInvalidQueryParam, []string{"sort"}, false},
		{"sort field is not sortable", http.MethodGet, "/widgets?sort=size:asc", "", http.StatusUnprocessableEntity, MsgInvalidQueryParam, []string{"sort"}, false},
		{"body sets the path id", http.MethodPatch, "/widgets/" + widgetID, `{"id":"other"}`, http.StatusBadRequest, MsgInvalidBody, []string{"id"}, false},
		{"body fails validation", http.MethodPost, "/widgets", `{}`, http.StatusUnprocessableEntity, MsgValidationFailed, []string{"description", "name"}, false},
		{"handler error", http.MethodGet, "/widgets/019f9b46-77bc-76ba-a494-000000000000", "", http.StatusNotFound, "Unable to get widget", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got any
			rec := serveWidget(widgetMux(&got), tc.method, tc.target, tc.body)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d (%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			body := decodeError(t, rec)
			if body.Message != tc.wantMessage {
				t.Errorf("message = %q, want %q", body.Message, tc.wantMessage)
			}
			if fields := slices.Sorted(maps.Keys(body.ValidationDetails)); !slices.Equal(fields, tc.wantFields) {
				t.Errorf("validationDetails fields = %v, want %v", fields, tc.wantFields)
			}
			if called := got != nil; called != tc.wantCalled {
				t.Errorf("handler called = %v, want %v", called, tc.wantCalled)
			}
		})
	}
}

func TestHandle_CallerError(t *testing.T) {
	var got any
	rec := serveWidget(widgetMux(&got), http.MethodPut, "/widgets/"+widgetID+"/publish", "")

	if rec.Code != http.StatusConflict || rec.Header().Get("Location") != "/widgets/"+widgetID {
		t.Fatalf("got %d Location %q, want 409 Location /widgets/%s", rec.Code, rec.Header().Get("Location"), widgetID)
	}
	if body := decodeError(t, rec); body.Message != "The widget is already published" {
		t.Errorf("message = %q, want the caller error's own", body.Message)
	}
}

func TestHandle_RetryAfter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /slow", Handle(func(context.Context, struct{}) (struct{}, error) {
		busy := &CallerError{Status: http.StatusServiceUnavailable, Message: "Took too long; retry shortly", Cause: errors.New("database busy")}
		return struct{}{}, RetryAfter(busy, 30*time.Second)
	}, "Unable to answer").serve)

	rec := serveWidget(mux, http.MethodGet, "/slow", "")

	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "30" {
		t.Fatalf("got %d Retry-After %q, want 503 and 30", rec.Code, rec.Header().Get("Retry-After"))
	}
	if body := decodeError(t, rec); body.Message != "Took too long; retry shortly" {
		t.Errorf("message = %q, want the wrapped error's own", body.Message)
	}
}

func TestHandle_LeavesAnAbandonedRequestUnanswered(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /cancelled", Handle(func(context.Context, struct{}) (widget, error) {
		return widget{}, fmt.Errorf("reading widgets: %w", context.Canceled)
	}, "Unable to list widgets").serve)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	gone := serveWidgetRequest(mux, httptest.NewRequestWithContext(ctx, http.MethodGet, "/cancelled", nil))
	if gone.Body.Len() != 0 || gone.Header().Get("Content-Type") != "" {
		t.Errorf("abandoned request answered %d %q, want nothing written", gone.Code, gone.Body.String())
	}

	waiting := serveWidget(mux, http.MethodGet, "/cancelled", "")
	if waiting.Code != http.StatusInternalServerError {
		t.Errorf("a cancellation with the client waiting = %d, want 500", waiting.Code)
	}

	mux.HandleFunc("GET /failed", Handle(func(context.Context, struct{}) (widget, error) {
		return widget{}, errors.New("reading widgets: disk full")
	}, "Unable to list widgets").serve)
	if failed := serveWidgetRequest(mux, httptest.NewRequestWithContext(ctx, http.MethodGet, "/failed", nil)); failed.Code != http.StatusInternalServerError {
		t.Errorf("another fault after the client went = %d, want 500", failed.Code)
	}
}

type strictListRequest struct {
	StrictRequest
	Page  Page      `json:"-"`
	Sort  SortOrder `sort:"label" json:"-"`
	Label string    `query:"label" json:"-"`
}

type strictNoteRequest struct {
	StrictRequest
	Text string `json:"text"`
}

func TestHandle_StrictRequestRefusesWhatItDoesNotRead(t *testing.T) {
	var got strictListRequest
	mux := http.NewServeMux()
	mux.HandleFunc("GET /strict", Handle(func(_ context.Context, req strictListRequest) (List[widget], error) {
		got = req
		return List[widget]{Items: []widget{}}, nil
	}, "Unable to list widgets").serve)
	mux.HandleFunc("PUT /strict", Handle(func(context.Context, strictNoteRequest) (struct{}, error) {
		return struct{}{}, nil
	}, "Unable to save note").serve)

	for _, tc := range []struct {
		name, method, target, body string
		wantStatus                 int
		wantDetail                 string
	}{
		{"a read request", http.MethodGet, "/strict?label=a&pageSize=2&sort=label:desc", "", http.StatusOK, ""},
		{"an empty filter is no filter", http.MethodGet, "/strict?label=", "", http.StatusOK, ""},
		{"a parameter it does not take", http.MethodGet, "/strict?lable=a", "", http.StatusBadRequest, "lable"},
		{"a filter given twice", http.MethodGet, "/strict?label=a&label=b", "", http.StatusBadRequest, "label"},
		{"a page size given twice", http.MethodGet, "/strict?pageSize=2&pageSize=3", "", http.StatusBadRequest, "pageSize"},
		{"a sort given twice", http.MethodGet, "/strict?sort=label:asc&sort=label:desc", "", http.StatusUnprocessableEntity, "sort"},
		{"a query that does not parse", http.MethodGet, "/strict?label=%zz", "", http.StatusBadRequest, "query"},
		{"a body on a request that reads none", http.MethodGet, "/strict", `{}`, http.StatusBadRequest, "body"},
		{"a null body on a request that reads none", http.MethodGet, "/strict", `null`, http.StatusBadRequest, "body"},
		{"a blank body on a request that reads none", http.MethodGet, "/strict", " \n", http.StatusOK, ""},
		{"a body request", http.MethodPut, "/strict", `{"text":"a"}`, http.StatusNoContent, ""},
		{"a parameter on a body request", http.MethodPut, "/strict?bogus=1", `{"text":"a"}`, http.StatusBadRequest, "bogus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got = strictListRequest{}
			rec := serveWidget(mux, tc.method, tc.target, tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantDetail != "" && len(decodeError(t, rec).ValidationDetails[tc.wantDetail]) == 0 {
				t.Errorf("no validationDetails for %q: %s", tc.wantDetail, rec.Body.String())
			}
		})
	}
	serveWidget(mux, http.MethodGet, "/strict?label=a&pageSize=2", "")
	if got.Label != "a" || got.Page.Size != 2 {
		t.Errorf("request = %+v, want label a and page size 2", got)
	}
}

func TestNewBinding_RejectsUnbindableTypes(t *testing.T) {
	for name, typ := range map[string]reflect.Type{
		"not a struct": reflect.TypeFor[string](),
		"path field the body can set": reflect.TypeFor[struct {
			ID string `path:"id"`
		}](),
		"unknown path option": reflect.TypeFor[struct {
			ID string `path:"id,slug" json:"-"`
		}](),
		"unknown query option": reflect.TypeFor[struct {
			Owner string `query:"owner,required" json:"-"`
		}](),
		"header field the body can set": reflect.TypeFor[struct {
			Surface string `header:"X-Sam-Client-Surface"`
		}](),
		"query field that is not a string": reflect.TypeFor[struct {
			Limit int `query:"limit" json:"-"`
		}](),
		"validate rule on a URL field": reflect.TypeFor[struct {
			Sort string `query:"sort" json:"-" validate:"required"`
		}](),
		"page the body can set": reflect.TypeFor[struct {
			Page Page
		}](),
		"sort without its fields": reflect.TypeFor[struct {
			Sort SortOrder `json:"-"`
		}](),
		"form field the JSON body can set": reflect.TypeFor[struct {
			File UploadedFile `form:"file"`
		}](),
		"form field that is neither a file nor a string": reflect.TypeFor[struct {
			Size int `form:"size" json:"-"`
		}](),
		"form and JSON body": reflect.TypeFor[struct {
			File UploadedFile `form:"file" json:"-"`
			Name string       `json:"name"`
		}](),
		"raw and form body": reflect.TypeFor[struct {
			File  UploadedFile `form:"file" json:"-"`
			Chunk RawBody      `json:"-"`
		}](),
		"path index that is not an int": reflect.TypeFor[struct {
			Index float64 `path:"id" json:"-"`
		}](),
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("newBinding accepted it")
				}
			}()
			newBinding(typ)
		})
	}
}

func TestHandle_WireAndHasBody(t *testing.T) {
	for name, tc := range map[string]struct {
		h       Handler
		wire    reflect.Type
		hasBody bool
	}{
		"list":      {stub[listWidgetsRequest, List[widget]](), reflect.TypeFor[PaginatedResponse[widget]](), false},
		"single":    {stub[getWidgetRequest, widget](), reflect.TypeFor[DataResponse[widget]](), false},
		"create":    {stub[createWidgetRequest, widget](), reflect.TypeFor[DataResponse[widget]](), true},
		"no output": {stub[getWidgetRequest, struct{}](), nil, false},
		"accepted":  {stub[getWidgetRequest, Accepted](), reflect.TypeFor[DataResponse[Operation]](), false},
		"download":  {stub[widgetFileRequest, Download](), nil, false},
		"file":      {stub[getWidgetRequest, FileResponse](), nil, false},
		"form body": {stub[uploadWidgetRequest, widget](), reflect.TypeFor[DataResponse[widget]](), false},
	} {
		if tc.h.Wire != tc.wire || tc.h.HasBody != tc.hasBody {
			t.Errorf("%s: Wire %v, HasBody %v; want %v, %v", name, tc.h.Wire, tc.h.HasBody, tc.wire, tc.hasBody)
		}
	}
}

type sealedWidget struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

func (sealedWidget) ResponseHeaders() map[string]string {
	return map[string]string{"Cache-Control": "no-store"}
}

func TestHandle_SendsTheResponseTypesHeadersOnSuccess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sealed", Handle(func(context.Context, createWidgetRequest) (sealedWidget, error) {
		return sealedWidget{ID: widgetID, Type: "widget"}, nil
	}, "Unable to create sealed widget").serve)
	mux.HandleFunc("GET /sealed/{id}", Handle(func(_ context.Context, req getWidgetRequest) (sealedWidget, error) {
		return sealedWidget{ID: req.ID, Type: "widget"}, nil
	}, "Unable to get sealed widget").serve)
	mux.HandleFunc("GET /sealed-missing/{id}", Handle(func(_ context.Context, req getWidgetRequest) (sealedWidget, error) {
		return sealedWidget{}, &apierr.NotFoundError{Resource: "widget", ID: req.ID}
	}, "Unable to get sealed widget").serve)

	for _, tc := range []struct {
		method, target, body string
		wantStatus           int
		wantNoStore          bool
	}{
		{http.MethodPost, "/sealed", `{"name":"a","description":"a long enough description"}`, http.StatusCreated, true},
		{http.MethodGet, "/sealed/" + widgetID, "", http.StatusOK, true},
		{http.MethodGet, "/sealed-missing/" + widgetID, "", http.StatusNotFound, false},
	} {
		rec := serveWidget(mux, tc.method, tc.target, tc.body)
		if rec.Code != tc.wantStatus {
			t.Fatalf("%s %s = %d, want %d; body %s", tc.method, tc.target, rec.Code, tc.wantStatus, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control") == "no-store"; got != tc.wantNoStore {
			t.Errorf("%s %s: Cache-Control no-store = %v, want %v", tc.method, tc.target, got, tc.wantNoStore)
		}
	}
}

type scopedListRequest struct {
	Owner string `query:"owner,nonempty" json:"-"`
	Label string `query:"label" json:"-"`
}

func TestHandle_NonEmptyQueryRefusesWhatWouldReadAsNoFilter(t *testing.T) {
	var got scopedListRequest
	mux := http.NewServeMux()
	mux.HandleFunc("GET /scoped", Handle(func(_ context.Context, req scopedListRequest) (List[widget], error) {
		got = req
		return List[widget]{Items: []widget{}}, nil
	}, "Unable to list widgets").serve)

	for _, tc := range []struct {
		name, target string
		wantStatus   int
		wantDetail   string
		wantOwner    string
	}{
		{"filter left out", "/scoped", http.StatusOK, "", ""},
		{"filter set", "/scoped?owner=a", http.StatusOK, "", "a"},
		{"filter present and empty", "/scoped?owner=", http.StatusBadRequest, "owner", ""},
		{"empty plain filter is no filter", "/scoped?label=", http.StatusOK, "", ""},
		{"filter repeated with an empty value", "/scoped?owner=a&owner=", http.StatusBadRequest, "owner", ""},
		{"query string that does not parse", "/scoped?owner=%zz", http.StatusBadRequest, "query", ""},
		{"misspelled filter", "/scoped?ownr=a", http.StatusBadRequest, "ownr", ""},
		{"page param on a request without a page", "/scoped?owner=a&pageSize=2", http.StatusBadRequest, "pageSize", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got = scopedListRequest{}
			rec := serveWidget(mux, http.MethodGet, tc.target, "")
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantDetail != "" && len(decodeError(t, rec).ValidationDetails[tc.wantDetail]) == 0 {
				t.Errorf("no validationDetails for %q: %s", tc.wantDetail, rec.Body.String())
			}
			if got.Owner != tc.wantOwner {
				t.Errorf("owner = %q, want %q", got.Owner, tc.wantOwner)
			}
		})
	}
}

type noteRequest struct {
	Text string `json:"text"`
}

func TestHandle_BodyOverCapSetOutsideDecoderIs413(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /notes", Handle(func(context.Context, noteRequest) (struct{}, error) {
		return struct{}{}, nil
	}, "Unable to save note").serve)

	req := httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader(`{"text":"`+strings.Repeat("x", 23)+`"}`))
	req.Body = http.MaxBytesReader(nil, req.Body, 16)
	rec := serveWidgetRequest(mux, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413 (%s)", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); body.Message != MsgBodyTooLarge {
		t.Errorf("message = %q, want %q", body.Message, MsgBodyTooLarge)
	}
}
