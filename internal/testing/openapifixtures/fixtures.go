// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapifixtures

import (
	"time"
)

type Widget struct {
	ID        string    `json:"id" example:"w-1"`
	Name      string    `json:"name" example:"Blue widget"`
	Count     int       `json:"count,omitempty" example:"3"`
	Legacy    string    `json:"legacy,omitempty" example:"Old widget"`
	Note      *string   `json:"note,omitempty" example:"Fragile"`
	CreatedAt Stamp     `json:"createdAt"`
	Tags      []string  `json:"tags" example:"[\"blue\"]"`
	Owner     Owner     `json:"owner"`
	Updated   time.Time `json:"updated,omitempty" example:"2022-03-14T15:01:34.000Z"`
	Secret    string    `json:"-"`
}

type Owner struct {
	Login string `json:"login" example:"alice"`
}

type Stamp int64

type CreateWidget struct {
	Name string `json:"name" example:"Blue widget"`
}

type Page[T any] struct {
	Data []T `json:"data"`
}

type Failure struct {
	Message string `json:"message" example:"Something failed"`
}

type Refusal struct {
	Message string `json:"message" example:"Authentication required"`
	Reason  string `json:"reason,omitempty" example:"authentication_required"`
}

type Undocumented struct {
	Field string `json:"field"`
}

type BadAnnotations struct {
	Old  string `json:"old"`
	Size int    `json:"size" example:"big"`
}

type Paging struct {
	PageNumber int `query:"pageNumber" default:"1" minimum:"1" example:"1"`
	PageSize   int `query:"pageSize" default:"20" minimum:"1" maximum:"100" example:"20"`
}

type WidgetQuery struct {
	Paging
	Owner  string   `query:"owner" required:"true" example:"alice"`
	Expand []string `query:"expand" example:"[\"owner\"]"`
	Colour string   `query:"colour" example:"blue"`
}

type BadQuery struct {
	Untagged string
	Nested   Owner `query:"nested"`
	Limit    int   `query:"limit" default:"lots"`
}

type RuleRequest struct {
	Name string `json:"name" validate:"required,min=3" example:"Blue widget"`
	Nick string `json:"nick" example:"Bluey"`
}

type HandlerCheckedRequest struct {
	Model *string `json:"model" required:"true" example:"general"`
}

type RequiredButNullableRequest struct {
	Limit *string `json:"limit" required:"true" clearable:"true" example:"250.00"`
}

type Optional[T any] struct {
	Value T
}

type ChangeRule struct {
	Rule Optional[RuleRequest] `json:"rule,omitempty"`
}

type Holder struct {
	Owner *Owner `json:"owner"`
}

type Listing struct {
	Labels map[string]string `json:"labels" nullable:"true" example:"{\"tier\":\"gold\"}"`
	Owners []Owner           `json:"owners" nullable:"true"`
	Tags   []string          `json:"tags" example:"[\"blue\"]"`
}

type OpenValue struct {
	Shape any `json:"shape" nullable:"true" example:"{\"type\":\"object\"}"`
}

type Envelope struct {
	Data Owner `json:"data"`
}

type PointerEnvelope struct {
	Data *Owner `json:"data"`
}

type Upload struct {
	File UploadedFile `form:"file" json:"-"`
	Note string       `form:"note" json:"-" example:"Weekly refresh"`
}

type UploadedFile struct {
	Name string `example:"widgets.zip"`
	Data []byte `example:"x"`
}

type Chunk struct {
	ID    int    `path:"id" json:"-"`
	Bytes []byte `json:"-"`
}

type Maybe struct {
	Next  *int `json:"next" example:"2"`
	Later *int `json:"later,omitempty" example:"3"`
}

type Checked struct {
	Label  string `json:"label" required:"true" example:"nightly"`
	Remark string `json:"remark" example:"none"`
}
