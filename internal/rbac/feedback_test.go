// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"reflect"
	"testing"
)

func TestSubjectScopeBuilders_Grammar(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"agent feedback", AgentReadFeedbackScope("hr-bot"), "agent:hr-bot:read_feedback"},
		{"workflow feedback", WorkflowReadFeedbackScope("3f2a1b4c-0000-4000-8000-000000000001"), "workflow:3f2a1b4c-0000-4000-8000-000000000001:read_feedback"},
		{"agent trace", AgentReadTraceScope("hr-bot"), "agent:hr-bot:read_trace"},
		{"agent feedback any", ScopeAgentReadFeedbackAny, "agent:*:read_feedback"},
		{"workflow feedback any", ScopeWorkflowReadFeedbackAny, "workflow:*:read_feedback"},
		{"agent trace any", ScopeAgentReadTraceAny, "agent:*:read_trace"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
		if details, ok := ValidateScopeShape([]string{c.got}); !ok {
			t.Errorf("%s = %q fails shape validation: %v", c.name, c.got, details)
		}
	}
}

func TestResolveFeedbackReach(t *testing.T) {
	sr := func(all bool, ids ...string) SubjectReach { return SubjectReach{All: all, IDs: ids} }
	tests := []struct {
		desc    string
		granted []string
		want    FeedbackReach
	}{
		{"nothing", nil, FeedbackReach{}},
		{"agent feedback wildcard sets only its own All", []string{ScopeAgentReadFeedbackAny}, FeedbackReach{AgentRead: sr(true)}},
		{"workflow feedback wildcard sets only its own All", []string{ScopeWorkflowReadFeedbackAny}, FeedbackReach{WorkflowRead: sr(true)}},
		{"agent trace wildcard sets only its own All", []string{ScopeAgentReadTraceAny}, FeedbackReach{AgentTrace: sr(true)}},
		{"per-subject agent feedback lands only in AgentRead", []string{AgentReadFeedbackScope("a")}, FeedbackReach{AgentRead: sr(false, "a")}},
		{"per-subject workflow feedback lands only in WorkflowRead", []string{WorkflowReadFeedbackScope("w")}, FeedbackReach{WorkflowRead: sr(false, "w")}},
		{"per-subject agent trace lands only in AgentTrace, no implies", []string{AgentReadTraceScope("a")}, FeedbackReach{AgentTrace: sr(false, "a")}},
		{"same id as agent and workflow read lands in both lists", []string{AgentReadFeedbackScope("x"), WorkflowReadFeedbackScope("x")}, FeedbackReach{AgentRead: sr(false, "x"), WorkflowRead: sr(false, "x")}},
		{"first-seen order per list", []string{AgentReadFeedbackScope("a"), AgentReadTraceScope("b"), AgentReadFeedbackScope("b"), AgentReadTraceScope("a")}, FeedbackReach{AgentRead: sr(false, "a", "b"), AgentTrace: sr(false, "b", "a")}},
		{"duplicates collapse", []string{AgentReadFeedbackScope("a"), AgentReadFeedbackScope("a")}, FeedbackReach{AgentRead: sr(false, "a")}},
		{"globbed ids drop in every category", []string{"agent:hr-*:read_feedback", "workflow:hr-*:read_feedback", "agent:hr-*:read_trace"}, FeedbackReach{}},
		{"a double-star glob is not a wildcard grant", []string{"agent:**:read_feedback", "workflow:**:read_feedback", "agent:**:read_trace"}, FeedbackReach{}},
		{"a question-mark glob is not an id", []string{"agent:?:read_feedback"}, FeedbackReach{}},
		{"a star kind on one id reaches neither kind", []string{"*:someid:read_feedback", "*:someid:read_trace"}, FeedbackReach{}},
		{"a partial-glob kind on one id reaches nothing", []string{"a*:someid:read_trace", "work*:someid:read_feedback", "?gent:someid:read_feedback"}, FeedbackReach{}},
		{"a partial-glob kind is not a wildcard grant", []string{"a*:*:read_trace", "a*:*:read_feedback"}, FeedbackReach{}},
		{"a star kind over every id still sets each kind's wildcard", []string{"*:*:read_feedback"}, FeedbackReach{AgentRead: sr(true), WorkflowRead: sr(true)}},
		{"collection sentinel canonicalizes to its own wildcard", []string{"agent:_:read_feedback", "workflow:_:read_feedback", "agent:_:read_trace"}, FeedbackReach{AgentRead: sr(true), WorkflowRead: sr(true), AgentTrace: sr(true)}},
		{"malformed grants ignored", []string{"agent:read_feedback", "agent:a:read_feedback:extra", "agent::read_feedback"}, FeedbackReach{}},
		{"agent:*:read does not reach the feedback or trace verbs", []string{"agent:*:read"}, FeedbackReach{}},
		{"activity:*:read reaches nothing here", []string{ScopeActivityRead}, FeedbackReach{}},
		{"invoke wildcards reach nothing here", []string{ScopeAgentInvokeAny, ScopeWorkflowInvokeAny}, FeedbackReach{}},
		{"the old feedback:*:read grants nothing", []string{"feedback:*:read"}, FeedbackReach{}},
		{"the old activity:*:read_content grants nothing", []string{"activity:*:read_content"}, FeedbackReach{}},
		{"bare superadmin star covers all three", []string{"*"}, FeedbackReach{AgentRead: sr(true), WorkflowRead: sr(true), AgentTrace: sr(true)}},
		{"universal three-segment wildcard covers all three", []string{"*:*:*"}, FeedbackReach{AgentRead: sr(true), WorkflowRead: sr(true), AgentTrace: sr(true)}},
		{"agent verb wildcard on one id covers that id's feedback and trace", []string{"agent:a:*"}, FeedbackReach{AgentRead: sr(false, "a"), AgentTrace: sr(false, "a")}},
		{"agent verb wildcard on every id covers every agent's feedback and trace", []string{"agent:*:*"}, FeedbackReach{AgentRead: sr(true), AgentTrace: sr(true)}},
		{"wildcard then per-subject: ids reset", []string{AgentReadFeedbackScope("a"), ScopeAgentReadFeedbackAny}, FeedbackReach{AgentRead: sr(true)}},
		{"per-subject then wildcard: ids reset", []string{ScopeAgentReadFeedbackAny, AgentReadFeedbackScope("a")}, FeedbackReach{AgentRead: sr(true)}},
		{"wildcard in one list leaves another list's ids alone", []string{ScopeAgentReadFeedbackAny, AgentReadTraceScope("a"), WorkflowReadFeedbackScope("w")}, FeedbackReach{AgentRead: sr(true), AgentTrace: sr(false, "a"), WorkflowRead: sr(false, "w")}},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := ResolveFeedbackReach(tt.granted)
			if !reflect.DeepEqual(normalize(got), normalize(tt.want)) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func normalize(r FeedbackReach) FeedbackReach {
	fix := func(s SubjectReach) SubjectReach {
		if len(s.IDs) == 0 {
			s.IDs = nil
		}
		return s
	}
	return FeedbackReach{AgentRead: fix(r.AgentRead), WorkflowRead: fix(r.WorkflowRead), AgentTrace: fix(r.AgentTrace)}
}

func TestFeedbackReach_Queries(t *testing.T) {
	r := FeedbackReach{
		AgentRead:    SubjectReach{IDs: []string{"a"}},
		WorkflowRead: SubjectReach{All: true},
		AgentTrace:   SubjectReach{IDs: []string{"a", "b"}},
	}
	tests := []struct {
		desc string
		got  bool
		want bool
	}{
		{"reads the listed agent", r.ReadsSubject(SubjectKindAgent, "a"), true},
		{"does not read an unlisted agent", r.ReadsSubject(SubjectKindAgent, "b"), false},
		{"reads any workflow via All", r.ReadsSubject(SubjectKindWorkflow, "anything"), true},
		{"unknown kind reads nothing", r.ReadsSubject("tool", "a"), false},
		{"legacy unknown-kind rows are unreachable even with every All set", FeedbackReach{AgentRead: SubjectReach{All: true}, WorkflowRead: SubjectReach{All: true}, AgentTrace: SubjectReach{All: true}}.ReadsSubject("unknown", "unknown"), false},
		{"a universal grant does not reach legacy unknown-kind rows", ResolveFeedbackReach([]string{"*"}).ReadsSubject("unknown", "unknown"), false},
		{"traces listed agents", r.TracesAgent("b"), true},
		{"does not trace an unlisted agent", r.TracesAgent("c"), false},
		{"trace All covers everything", FeedbackReach{AgentTrace: SubjectReach{All: true}}.TracesAgent("zzz"), true},
		{"empty reach reads nothing", FeedbackReach{}.ReadsSubject(SubjectKindAgent, "a"), false},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.desc, tt.got, tt.want)
		}
	}
}
