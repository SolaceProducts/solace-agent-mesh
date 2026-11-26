// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eventcatalog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	eventCatalogEntryResourceType = "eventCatalogEntry"
	eventCatalogEntriesPath       = "/eventCatalogEntries"
	eventCatalogEntryPath         = eventCatalogEntriesPath + "/{id}"
	eventCatalogMaxBodyBytes      = 4 * eventcatalog.MaxDocumentBytes
)

var curateEventCatalogScopes = []string{rbac.ScopeEventCatalogCreate, rbac.ScopeEventCatalogUpdate, rbac.ScopeEventCatalogDelete}

type EventCatalogEntry struct {
	ID             string         `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e50"`
	Type           string         `json:"type" example:"eventCatalogEntry"`
	Name           string         `json:"name" example:"Shipment delayed"`
	Key            string         `json:"key" example:"shipment_delayed"`
	Description    string         `json:"description" example:"A carrier reports that a shipment will arrive late."`
	EntrypointID   *string        `json:"entrypointId" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	TopicPattern   *string        `json:"topicPattern" example:"acme/logistics/shipment/*/delayed"`
	PayloadSchema  map[string]any `json:"payloadSchema" nullable:"true" example:"{\"type\":\"object\",\"required\":[\"shipmentId\"],\"properties\":{\"shipmentId\":{\"type\":\"string\"},\"delayMinutes\":{\"type\":\"integer\"}}}"`
	ExamplePayload map[string]any `json:"examplePayload" nullable:"true" example:"{\"shipmentId\":\"SHP-1001\",\"delayMinutes\":90}"`
	Enabled        bool           `json:"enabled" example:"true"`
	CreatedBy      string         `json:"createdBy" example:"alice@example.com"`
	UpdatedBy      string         `json:"updatedBy" example:"bob@example.com"`
	CreatedTime    Instant        `json:"createdTime"`
	UpdatedTime    Instant        `json:"updatedTime"`
}

type CreateEventCatalogEntryRequest struct {
	Name           string         `json:"name" required:"true" example:"Shipment delayed"`
	Key            string         `json:"key" required:"true" example:"shipment_delayed"`
	Description    string         `json:"description" required:"true" example:"A carrier reports that a shipment will arrive late."`
	EntrypointID   string         `json:"entrypointId" required:"true" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	TopicPattern   string         `json:"topicPattern" required:"true" example:"acme/logistics/shipment/*/delayed"`
	PayloadSchema  map[string]any `json:"payloadSchema" nullable:"true" example:"{\"type\":\"object\",\"required\":[\"shipmentId\"],\"properties\":{\"shipmentId\":{\"type\":\"string\"}}}"`
	ExamplePayload map[string]any `json:"examplePayload" nullable:"true" example:"{\"shipmentId\":\"SHP-1001\"}"`
	Enabled        *bool          `json:"enabled" example:"true"`
}

type UpdateEventCatalogEntryRequest struct {
	ID             string                `path:"id" json:"-"`
	Name           Patch[string]         `json:"name" example:"Shipment delayed"`
	Key            Patch[string]         `json:"key" example:"shipment_delayed"`
	Description    Patch[string]         `json:"description" example:"A carrier reports that a shipment will arrive late."`
	EntrypointID   Patch[string]         `json:"entrypointId" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	TopicPattern   Patch[string]         `json:"topicPattern" example:"acme/logistics/shipment/*/delayed"`
	PayloadSchema  Patch[map[string]any] `json:"payloadSchema" clearable:"true" example:"{\"type\":\"object\",\"required\":[\"shipmentId\"],\"properties\":{\"shipmentId\":{\"type\":\"string\"}}}"`
	ExamplePayload Patch[map[string]any] `json:"examplePayload" clearable:"true" example:"{\"shipmentId\":\"SHP-1001\"}"`
	Enabled        Patch[bool]           `json:"enabled" example:"false"`
}

type listEventCatalogEntriesRequest struct {
	Page         Page      `json:"-"`
	Enabled      string    `query:"enabled" json:"-" example:"true"`
	EntrypointID string    `query:"entrypointId" json:"-" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	Sort         SortOrder `sort:"name,key,createdTime,updatedTime" json:"-"`
}

type eventCatalogHandler struct {
	svc *eventcatalog.Service
}

func eventCatalogRoutes(deps *entplatform.RouteDeps) []Route {
	if deps.EventCatalogSvc == nil {
		return nil
	}
	h := &eventCatalogHandler{svc: deps.EventCatalogSvc}
	return []Route{
		{Method: http.MethodGet, Path: eventCatalogEntriesPath, Scope: rbac.ScopeEventCatalogRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "listEventCatalogEntries", Summary: "Get a list of event catalog entries",
				Text: "Returns one page of the event catalog. A caller who holds none of the event catalog's create, update or delete scopes gets only the enabled entries, with topicPattern and entrypointId null.", Tag: eventCatalogEntriesTag},
			Handler: Handle(h.list, "Unable to list event catalog entries")},
		{Method: http.MethodPost, Path: eventCatalogEntriesPath, Scope: rbac.ScopeEventCatalogCreate,
			Stability: StabilityDraft, MaxBodyBytes: eventCatalogMaxBodyBytes,
			Doc: Doc{ID: "createEventCatalogEntry", Summary: "Create an event catalog entry",
				Text: "Adds a named business event to the catalog, mapped to a topic on an event mesh entrypoint's broker. Every problem with the request is reported at once. If another entry has the name or the key, and nothing else is wrong, the request fails with 409.", Tag: eventCatalogEntriesTag,
				Errors: []int{http.StatusRequestEntityTooLarge}},
			Handler: Handle(h.create, "Unable to create event catalog entry")},
		{Method: http.MethodGet, Path: eventCatalogEntryPath, Scope: rbac.ScopeEventCatalogRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getEventCatalogEntry", Summary: "Get an event catalog entry",
				Text: "Returns one entry of the event catalog. A caller who holds none of the event catalog's create, update or delete scopes gets 404 for a disabled entry, and topicPattern and entrypointId null.", Tag: eventCatalogEntriesTag},
			Handler: Handle(h.get, "Unable to get event catalog entry")},
		{Method: http.MethodPatch, Path: eventCatalogEntryPath, Scope: rbac.ScopeEventCatalogUpdate,
			Stability: StabilityDraft, MaxBodyBytes: eventCatalogMaxBodyBytes,
			Doc: Doc{ID: "updateEventCatalogEntry", Summary: "Update an event catalog entry",
				Text: "Changes an entry of the event catalog, including enabling or disabling it. Its key cannot be changed. The entrypoint and the topic are checked again only when the request changes either of them or enables the entry, so an entry whose entrypoint was deleted can still be disabled, renamed or deleted. Every problem with the request is reported at once. If another entry has the new name, and nothing else is wrong, the request fails with 409.", Tag: eventCatalogEntriesTag,
				Errors: []int{http.StatusRequestEntityTooLarge}},
			Handler: Handle(h.update, "Unable to update event catalog entry")},
		{Method: http.MethodDelete, Path: eventCatalogEntryPath, Scope: rbac.ScopeEventCatalogDelete,
			Stability: StabilityDraft,
			Doc: Doc{ID: "deleteEventCatalogEntry", Summary: "Delete an event catalog entry",
				Text: "Removes an entry from the event catalog.", Tag: eventCatalogEntriesTag},
			Handler: Handle(h.delete, "Unable to delete event catalog entry")},
	}
}

func (h *eventCatalogHandler) list(ctx context.Context, req listEventCatalogEntriesRequest) (List[EventCatalogEntry], error) {
	if err := CheckUUIDQuery("entrypointId", req.EntrypointID); err != nil {
		return List[EventCatalogEntry]{}, err
	}
	enabled, err := enabledFilter(req.Enabled)
	if err != nil {
		return List[EventCatalogEntry]{}, err
	}
	curator := curatesEventCatalog(ctx)
	if !curator {
		if req.EntrypointID != "" {
			return List[EventCatalogEntry]{}, &CallerError{Status: http.StatusForbidden, Message: msgNoPermission}
		}
		if enabled != nil && !*enabled {
			return List[EventCatalogEntry]{Items: []EventCatalogEntry{}}, nil
		}
		enabled = new(true)
	}
	entries, total, err := h.svc.List(ctx, eventcatalog.ListOptions{
		Enabled: enabled, EntrypointID: req.EntrypointID,
		PageQuery: store.PageQuery{Sort: req.Sort.Field, Desc: req.Sort.Desc, Offset: (req.Page.Number - 1) * req.Page.Size, Limit: req.Page.Size},
	})
	if err != nil {
		return List[EventCatalogEntry]{}, err
	}
	items := make([]EventCatalogEntry, len(entries))
	for i, e := range entries {
		items[i] = toEventCatalogEntry(e, curator)
	}
	return List[EventCatalogEntry]{Items: items, Total: total}, nil
}

func enabledFilter(value string) (*bool, error) {
	switch value {
	case "":
		return nil, nil
	case "true":
		return new(true), nil
	case "false":
		return new(false), nil
	default:
		return nil, QueryError(http.StatusBadRequest, "enabled", "must be true or false")
	}
}

func (h *eventCatalogHandler) create(ctx context.Context, req CreateEventCatalogEntryRequest) (EventCatalogEntry, error) {
	enabled := req.Enabled == nil || *req.Enabled
	entry, err := h.svc.Create(ctx, eventcatalog.Fields{
		Name: req.Name, Key: req.Key, Description: req.Description, EntrypointID: req.EntrypointID,
		TopicPattern: req.TopicPattern, PayloadSchema: req.PayloadSchema, ExamplePayload: req.ExamplePayload, Enabled: enabled,
	}, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return EventCatalogEntry{}, takenConflict(err)
	}
	return toEventCatalogEntry(entry, true), nil
}

func (h *eventCatalogHandler) get(ctx context.Context, req IDRequest) (EventCatalogEntry, error) {
	entry, err := h.svc.Get(ctx, req.ID)
	if err != nil {
		return EventCatalogEntry{}, err
	}
	curator := curatesEventCatalog(ctx)
	if !curator && !entry.Enabled {
		return EventCatalogEntry{}, &apierr.NotFoundError{Resource: "event catalog entry", ID: req.ID}
	}
	return toEventCatalogEntry(entry, curator), nil
}

func (h *eventCatalogHandler) update(ctx context.Context, req UpdateEventCatalogEntryRequest) (EventCatalogEntry, error) {
	entry, err := h.svc.Update(ctx, req.ID, req.toChange(), entplatform.UserIDFromContext(ctx))
	if err != nil {
		return EventCatalogEntry{}, takenConflict(err)
	}
	return toEventCatalogEntry(entry, true), nil
}

func (req UpdateEventCatalogEntryRequest) toChange() eventcatalog.Change {
	problems := map[string][]string{}
	if req.Key.Set {
		problems[eventcatalog.FieldKey] = []string{"cannot be changed"}
	}
	if req.Enabled.Set && req.Enabled.Null {
		problems[eventcatalog.FieldEnabled] = []string{"cannot be null"}
	}
	change := eventcatalog.Change{
		Name:              requiredText(problems, eventcatalog.FieldName, req.Name),
		Description:       requiredText(problems, eventcatalog.FieldDescription, req.Description),
		EntrypointID:      requiredText(problems, eventcatalog.FieldEntrypointID, req.EntrypointID),
		TopicPattern:      requiredText(problems, eventcatalog.FieldTopicPattern, req.TopicPattern),
		SetPayloadSchema:  req.PayloadSchema.Set,
		PayloadSchema:     req.PayloadSchema.Value,
		SetExamplePayload: req.ExamplePayload.Set,
		ExamplePayload:    req.ExamplePayload.Value,
		Problems:          problems,
	}
	if req.Enabled.Set && !req.Enabled.Null {
		change.Enabled = &req.Enabled.Value
	}
	return change
}

func requiredText(problems map[string][]string, field string, p Patch[string]) *string {
	if !p.Set {
		return nil
	}
	if p.Null || p.Value == "" {
		problems[field] = []string{"cannot be empty"}
		return nil
	}
	return &p.Value
}

func (h *eventCatalogHandler) delete(ctx context.Context, req IDRequest) (struct{}, error) {
	return struct{}{}, h.svc.Delete(ctx, req.ID)
}

func takenConflict(err error) error {
	if taken, ok := errors.AsType[*eventcatalog.TakenError](err); ok {
		return &CallerError{Status: http.StatusConflict, Message: taken.Error(), Details: taken.Details, Cause: err}
	}
	return err
}

func curatesEventCatalog(ctx context.Context) bool {
	caller, ok := auth.SubjectFromContext(ctx)
	return ok && caller != nil && slices.ContainsFunc(curateEventCatalogScopes, func(scope string) bool { return caller.HasScope(scope) })
}

func toEventCatalogEntry(e *eventcatalog.Entry, curator bool) EventCatalogEntry {
	out := EventCatalogEntry{
		ID: e.ID, Type: eventCatalogEntryResourceType, Name: e.Name, Key: e.Key, Description: e.Description,
		PayloadSchema: e.PayloadSchema, ExamplePayload: e.ExamplePayload, Enabled: e.Enabled,
		CreatedBy: e.CreatedBy, UpdatedBy: e.UpdatedBy,
		CreatedTime: Instant(e.CreatedTime), UpdatedTime: Instant(e.UpdatedTime),
	}
	if curator {
		out.EntrypointID, out.TopicPattern = &e.EntrypointID, &e.TopicPattern
	}
	return out
}
