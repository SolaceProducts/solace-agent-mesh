// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"crypto/hmac"
	"crypto/sha256"
	"time"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const EventSchemaVersion = 1

const TimestampLayout = "2006-01-02T15:04:05.000Z07:00"

type RawEvent struct {
	EventID    string
	SessionID  string
	OccurredAt string
	Name       string
	Properties map[string]any
}

type Event struct {
	EventID       string
	DistinctID    string
	SessionID     string
	PublisherID   string
	Seq           uint64
	Name          string
	OccurredAt    time.Time
	ReceivedAt    time.Time
	SchemaVersion int
	Properties    map[string]any

	traits   map[string]any
	admitted uint64
}

const (
	idDomainEvent   = "eventId"
	idDomainSession = "sessionId"
	idDomainTrace   = "traceId"
)

func boundID(identity Identity, domain, clientID string) string {
	mac := hmac.New(sha256.New, identity.BindingKey)
	_, _ = mac.Write([]byte(domain))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(identity.Marker))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(clientID))

	var id uuid.UUID
	copy(id[:], mac.Sum(nil))
	id[6] = id[6]&0x0f | 0x80
	id[8] = id[8]&0x3f | 0x80
	return id.String()
}

func eventIDOrMint(identity Identity, clientID string) string {
	if reUUID.MatchString(clientID) {
		return boundID(identity, idDomainEvent, clientID)
	}
	return idgen.New()
}

func sessionIDOrEmpty(identity Identity, clientID string) string {
	if reUUID.MatchString(clientID) {
		return boundID(identity, idDomainSession, clientID)
	}
	return ""
}

func bindTrace(props map[string]any, identity Identity) {
	raw, ok := props[PropTraceID].(string)
	if !ok {
		return
	}
	props[PropTraceID] = boundID(identity, idDomainTrace, raw)
}

func parseOccurredAt(clientTime string) (time.Time, bool) {
	if clientTime == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, clientTime)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC().Truncate(time.Millisecond), true
}
