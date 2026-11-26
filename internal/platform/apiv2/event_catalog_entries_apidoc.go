// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (EventCatalogEntry) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `EventCatalogEntry is a named business event that an always-on agent can wake
on, mapped to a topic on the broker of an event mesh entrypoint.`,
		Fields: map[string]apidoc.Field{
			"ID":             {Doc: "ID is the entry's UUID."},
			"Type":           {Doc: "Type is always \"eventCatalogEntry\"."},
			"Name":           {Doc: "Name is the label people choose the event by, unique ignoring case."},
			"Key":            {Doc: "Key identifies the event in configuration: lowercase snake_case, unique, and fixed when the entry is created."},
			"Description":    {Doc: "Description says what happened when the event arrives."},
			"EntrypointID":   {Doc: "EntrypointID is the UUID of the event mesh entrypoint whose broker carries the event. Null for a caller who holds none of the event catalog's create, update or delete scopes."},
			"TopicPattern":   {Doc: "TopicPattern is the topic subscription that selects the event on that broker: levels separated by /, * for one level or the rest of a level, and > as the last level for one or more levels. Null for a caller who holds none of the event catalog's create, update or delete scopes."},
			"PayloadSchema":  {Doc: "PayloadSchema is the JSON Schema the event's payload follows. Null when none is set."},
			"ExamplePayload": {Doc: "ExamplePayload is a sample payload of the event, shown as it was stored. It is documentation: it is not checked against payloadSchema. Null when none is set."},
			"Enabled":        {Doc: "Enabled is true when owners can choose the event. A caller who holds none of the event catalog's create, update or delete scopes sees only enabled entries."},
			"CreatedBy":      {Doc: "CreatedBy identifies the user who created the entry: their email, or their user ID when no email is known."},
			"UpdatedBy":      {Doc: "UpdatedBy identifies the user who last changed the entry, in the same form as createdBy."},
			"CreatedTime":    {Doc: "CreatedTime is when the entry was created."},
			"UpdatedTime":    {Doc: "UpdatedTime is when the entry was last changed."},
		},
	}
}

func (CreateEventCatalogEntryRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateEventCatalogEntryRequest is the body that adds an event to the catalog.
Every problem with it is reported at once.`,
		Fields: map[string]apidoc.Field{
			"Name":           {Doc: "Name is the label people choose the event by, 1 to 255 characters and unique ignoring case."},
			"Key":            {Doc: "Key identifies the event in configuration, at most 64 characters of lowercase snake_case, unique. It cannot be changed later."},
			"Description":    {Doc: "Description says what happened when the event arrives, 1 to 2,000 characters."},
			"EntrypointID":   {Doc: "EntrypointID is the UUID of the event mesh entrypoint whose broker carries the event."},
			"TopicPattern":   {Doc: "TopicPattern is the topic subscription that selects the event, at most 250 bytes: levels separated by /, with no empty level, * for one level or the rest of a level, and > only as the last level. It must not start with #."},
			"PayloadSchema":  {Doc: "PayloadSchema is the JSON Schema the event's payload follows, a self-contained document of at most 64 KiB. It cannot use $ref, $dynamicRef or $recursiveRef. Every object and every array counts as one level, so it can nest at most 32 levels, which is about 15 levels of properties inside properties. It can hold at most 256 subschemas, counting schema objects and the true and false schemas that sit in arrays such as anyOf. It can hold at most 16 pattern and patternProperties keywords in total, counting each patternProperties entry, and each regular expression must be at most 256 bytes and compile to at most 256 steps, which allows ordinary formats such as a UUID or a date but not large repeat counts. Leave it out for none."},
			"ExamplePayload": {Doc: "ExamplePayload is a sample payload of at most 8 KiB, stored as given. It is documentation, so it is not checked against payloadSchema and may differ from it. Every object and every array counts as one level, and it can nest at most 32 levels. Leave it out for none."},
			"Enabled":        {Doc: "Enabled is whether owners can choose the event. Leave it out to enable it."},
		},
	}
}

func (UpdateEventCatalogEntryRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateEventCatalogEntryRequest changes only the fields it includes. The entry
that results is checked as a whole, so a field that is invalid next to the
others fails with 422. The stored examplePayload is not checked against a new
payloadSchema.`,
		Fields: map[string]apidoc.Field{
			"ID":             {Doc: "ID is the UUID of the entry to update."},
			"Name":           {Doc: "Name is the label people choose the event by. Leave it out to keep it. Null or an empty string fails with 422."},
			"Key":            {Doc: "Key is fixed when the entry is created. Including it fails with 422."},
			"Description":    {Doc: "Description says what happened when the event arrives. Leave it out to keep it. Null or an empty string fails with 422."},
			"EntrypointID":   {Doc: "EntrypointID is the UUID of the event mesh entrypoint whose broker carries the event. Leave it out to keep it. Null or an empty string fails with 422."},
			"TopicPattern":   {Doc: "TopicPattern is the topic subscription that selects the event. Leave it out to keep it. Null or an empty string fails with 422."},
			"PayloadSchema":  {Doc: "PayloadSchema replaces the JSON Schema the event's payload follows, under the same limits as when the entry is created. Leave it out to keep it, or send null to clear it."},
			"ExamplePayload": {Doc: "ExamplePayload replaces the sample payload, under the same limits as when the entry is created. It is stored as given and not checked against payloadSchema. Leave it out to keep it, or send null to clear it."},
			"Enabled":        {Doc: "Enabled is whether owners can choose the event. Leave it out to keep it. Null fails with 422."},
		},
	}
}

func (listEventCatalogEntriesRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listEventCatalogEntriesRequest selects a page of the catalog.",
		Fields: map[string]apidoc.Field{
			"Page":         {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
			"Enabled":      {Doc: "Enabled limits the list to enabled (true) or disabled (false) entries. Leave it out to list both, as far as the caller may see them."},
			"EntrypointID": {Doc: "EntrypointID limits the list to the entries on this event mesh entrypoint. Filtering by it fails with 403 for a caller who holds none of the event catalog's create, update or delete scopes."},
			"Sort":         {Doc: "Sort orders the list by name, key, createdTime or updatedTime, as field:asc or field:desc; leave it out for name order."},
		},
	}
}
