// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import (
	"cmp"
	"errors"
	"mime"
	"net/http"
	"reflect"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
)

func WriteResource[T any](w http.ResponseWriter, status int, resource T) {
	WriteJSON(w, status, DataResponse[T]{Data: resource})
}

func WriteList[T any](w http.ResponseWriter, items []T, p Page, total int) {
	WriteJSON(w, http.StatusOK, PaginatedResponse[T]{
		Data: NonNilList(items),
		Meta: NewPaginationMeta(p.Number, p.Size, total),
	})
}

func NonNilList[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

func PageOf[T any](items []T, p Page) List[T] {
	start := min((p.Number-1)*p.Size, len(items))
	end := min(start+p.Size, len(items))
	return List[T]{Items: items[start:end], Total: len(items)}
}

type Accepted struct {
	Operation Operation
	Location  string
}

func (a Accepted) WriteTo(w http.ResponseWriter) {
	w.Header().Set("Location", a.Location)
	WriteResource(w, a.status(), a.Operation)
}

func (Accepted) status() int { return http.StatusAccepted }

func (Accepted) wireType() reflect.Type {
	return reflect.TypeFor[DataResponse[Operation]]()
}

type Download struct {
	Content  []byte
	MimeType string
	Filename string
}

func (d Download) WriteTo(w http.ResponseWriter) {
	w.Header().Set(headerContentType, cmp.Or(d.MimeType, "application/octet-stream"))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": d.Filename}))
	w.WriteHeader(d.status())
	_, _ = w.Write(d.Content)
}

func (Download) wireType() reflect.Type { return nil }

func (Download) status() int { return http.StatusOK }

func WriteServiceError(w http.ResponseWriter, err error, failure string) {
	status, details := ClassifyServiceError(err)
	WriteErrorWithCause(w, status, failure, details, err)
}

func ClassifyServiceError(err error) (int, map[string][]string) {
	if e, ok := errors.AsType[*apierr.ValidationError](err); ok {
		if len(e.Details) == 0 && e.Field != "" {
			return http.StatusUnprocessableEntity, map[string][]string{e.Field: {e.Message}}
		}
		return http.StatusUnprocessableEntity, e.Details
	}
	switch {
	case IsErr[*apierr.BusinessRuleViolationError](err):
		return http.StatusUnprocessableEntity, nil
	case IsErr[*apierr.NotFoundError](err):
		return http.StatusNotFound, nil
	case IsErr[*apierr.ConflictError](err):
		return http.StatusConflict, nil
	case IsErr[*apierr.ForbiddenError](err):
		return http.StatusForbidden, nil
	case IsErr[*apierr.ExternalServiceError](err):
		return http.StatusBadGateway, nil
	case IsErr[*apierr.UnavailableError](err):
		return http.StatusServiceUnavailable, nil
	default:
		return http.StatusInternalServerError, nil
	}
}

func IsErr[T error](err error) bool {
	_, ok := errors.AsType[T](err)
	return ok
}

const valuesKeyPrefix = "values."

func UnderValues(err error) error {
	ve, ok := errors.AsType[*apierr.ValidationError](err)
	if !ok || len(ve.Details) == 0 {
		return err
	}
	return &apierr.ValidationError{Message: ve.Message, Details: settingsUnderValues(ve.Details)}
}

func settingsUnderValues(details map[string][]string) map[string][]string {
	filed := make(map[string][]string, len(details))
	for setting, problems := range details {
		filed[valuesKeyPrefix+setting] = problems
	}
	return filed
}

type Upserted[T any] struct {
	Resource T
	Created  bool
}

func (u Upserted[T]) WriteTo(w http.ResponseWriter) {
	status := u.status()
	if u.Created {
		status = u.createdStatus()
	}
	WriteResource(w, status, u.Resource)
}

func (Upserted[T]) status() int { return http.StatusOK }

func (Upserted[T]) wireType() reflect.Type { return reflect.TypeFor[DataResponse[T]]() }

func (Upserted[T]) createdStatus() int { return http.StatusCreated }

type CreatingUpsert interface {
	createdStatus() int
}

type Optional[T any] struct {
	Value *T
}

func (o Optional[T]) WriteTo(w http.ResponseWriter) {
	if o.Value == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	WriteResource(w, http.StatusOK, *o.Value)
}

func (Optional[T]) status() int { return http.StatusOK }

func (Optional[T]) mayBeAbsent() bool { return true }

func (Optional[T]) wireType() reflect.Type { return reflect.TypeFor[DataResponse[T]]() }
