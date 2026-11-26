// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"testing"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
)

func choiceSurface(labels ...string) *a2ui.Surface {
	opts := make([]a2ui.Option, len(labels))
	for i, l := range labels {
		opts[i] = a2ui.Option{Label: l, Value: l}
	}
	return &a2ui.Surface{
		Fields: []a2ui.Field{{Kind: a2ui.FieldSingleChoice, Path: "/answers/q0", Options: opts}},
	}
}

func askSurface(labels ...string) *a2ui.Surface {
	opts := make([]a2ui.Option, 0, len(labels)+1)
	for _, l := range labels {
		opts = append(opts, a2ui.Option{Label: l, Value: l})
	}
	opts = append(opts, a2ui.Option{Label: "Other", Value: a2ui.OtherSentinel})
	return &a2ui.Surface{
		Fields: []a2ui.Field{
			{Kind: a2ui.FieldSingleChoice, Path: "/answers/q0", Options: opts},
			{Kind: a2ui.FieldText, Path: "/other_text/q0"},
		},
	}
}

func TestClassifySurface_AskUserQuestionShape(t *testing.T) {
	mode, f := classifySurface(askSurface("Pizza", "Sushi", "Tacos"))
	if mode != hilButtons || f == nil {
		t.Fatalf("ask shape (3): mode=%v field=%v, want buttons", mode, f)
	}
	btns := buttonsForField(f, askSurface("Pizza", "Sushi", "Tacos"))
	if len(btns) != 3 {
		t.Errorf("buttons = %d, want 3 (Other excluded)", len(btns))
	}
	for _, b := range btns {
		if b.ID == optionRefID(3) {
			t.Error("Other option must not be rendered as a button")
		}
	}
	if mode, _ := classifySurface(askSurface("a", "b", "c", "d", "e")); mode != hilList {
		t.Errorf("ask shape (5): mode=%v, want list", mode)
	}
}

func TestClassifySurface(t *testing.T) {
	if mode, f := classifySurface(choiceSurface("Yes", "No")); mode != hilButtons || f == nil {
		t.Errorf("2 options: mode=%v field=%v, want buttons + field", mode, f)
	}
	if mode, _ := classifySurface(choiceSurface("A", "B", "C")); mode != hilButtons {
		t.Errorf("3 options: mode=%v, want buttons", mode)
	}
	if mode, _ := classifySurface(choiceSurface("A", "B", "C", "D")); mode != hilList {
		t.Errorf("4 options: mode=%v, want list", mode)
	}
	if mode, _ := classifySurface(choiceSurface("1", "2", "3", "4", "5", "6", "7", "8", "9", "10")); mode != hilList {
		t.Errorf("10 options: mode=%v, want list", mode)
	}
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = "opt"
	}
	if mode, _ := classifySurface(choiceSurface(eleven...)); mode != hilText {
		t.Errorf("11 options: mode=%v, want text", mode)
	}

	textSurf := &a2ui.Surface{Fields: []a2ui.Field{{Kind: a2ui.FieldText, Path: "/answers/q0"}}}
	if mode, _ := classifySurface(textSurf); mode != hilText {
		t.Errorf("text field: mode=%v, want text", mode)
	}
	multi := &a2ui.Surface{Fields: []a2ui.Field{{Kind: a2ui.FieldMultiChoice, Options: []a2ui.Option{{Label: "A"}}}}}
	if mode, _ := classifySurface(multi); mode != hilText {
		t.Errorf("multi field: mode=%v, want text", mode)
	}
	two := &a2ui.Surface{Fields: []a2ui.Field{
		{Kind: a2ui.FieldSingleChoice, Options: []a2ui.Option{{Label: "A"}}},
		{Kind: a2ui.FieldSingleChoice, Options: []a2ui.Option{{Label: "B"}}},
	}}
	if mode, _ := classifySurface(two); mode != hilText {
		t.Errorf("two fields: mode=%v, want text", mode)
	}
	long := choiceSurface("this option label is definitely longer than twenty-four characters")
	if mode, _ := classifySurface(long); mode != hilText {
		t.Errorf("long label: mode=%v, want text", mode)
	}
}

func TestParseReplyRef(t *testing.T) {
	if idx, cancel, ok := parseReplyRef("opt:2"); !ok || cancel || idx != 2 {
		t.Errorf("opt:2 → idx=%d cancel=%v ok=%v", idx, cancel, ok)
	}
	if _, cancel, ok := parseReplyRef("cancel"); !ok || !cancel {
		t.Errorf("cancel → cancel=%v ok=%v", cancel, ok)
	}
	if _, _, ok := parseReplyRef("garbage"); ok {
		t.Error("garbage parsed as a valid ref")
	}
	if _, _, ok := parseReplyRef("opt:x"); ok {
		t.Error("opt:x parsed as a valid ref")
	}
}

func TestButtonsForField_WithCancel(t *testing.T) {
	s := choiceSurface("Yes", "No")
	s.Buttons = []a2ui.Button{{Event: "submit"}, {Event: "cancel"}}
	btns := buttonsForField(&s.Fields[0], s)
	if len(btns) != 3 || btns[0].ID != "opt:0" || btns[2].ID != cancelRefID {
		t.Fatalf("buttons = %+v, want opt:0, opt:1, cancel", btns)
	}

	s3 := choiceSurface("A", "B", "C")
	s3.Buttons = []a2ui.Button{{Event: "submit"}, {Event: "cancel"}}
	btns3 := buttonsForField(&s3.Fields[0], s3)
	if len(btns3) != 3 || btns3[2].ID != "opt:2" {
		t.Errorf("3-option buttons = %+v, want no cancel", btns3)
	}
}

func TestRowsForField(t *testing.T) {
	s := choiceSurface("Apple", "Banana", "Cherry", "Date")
	rows := rowsForField(&s.Fields[0], s)
	if len(rows) != 4 || rows[0].ID != "opt:0" || rows[3].Title != "Date" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestRowsForField_AppendsCancelRow(t *testing.T) {
	s := choiceSurface("Apple", "Banana", "Cherry", "Date")
	s.Buttons = []a2ui.Button{{Event: "submit"}, {Event: "cancel"}}
	rows := rowsForField(&s.Fields[0], s)
	if len(rows) != 5 || rows[4].ID != cancelRefID || rows[4].Title != "Cancel" {
		t.Fatalf("rows = %+v, want trailing cancel row", rows)
	}

	ten := choiceSurface("A", "B", "C", "D", "E", "F", "G", "H", "I", "J")
	ten.Buttons = []a2ui.Button{{Event: "submit"}, {Event: "cancel"}}
	full := rowsForField(&ten.Fields[0], ten)
	if len(full) != waMaxListRows || full[len(full)-1].ID == cancelRefID {
		t.Fatalf("10-option list must drop the cancel row, got %+v", full)
	}
}

func TestTruncate_RuneSafe(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("short string truncated: %q", got)
	}
	if got := truncate("hello world", 5); got != "hell…" {
		t.Errorf("ascii truncate = %q, want hell…", got)
	}
	got := truncate("🍕🍣🌮🍔🍟", 3)
	if got != "🍕🍣…" {
		t.Errorf("emoji truncate = %q, want 🍕🍣…", got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncate produced invalid UTF-8: %q", got)
	}
	label := "café société"
	if got := truncate(label, 12); got != label {
		t.Errorf("rune-count label wrongly truncated: %q", got)
	}
}

func TestInteractiveReplyID(t *testing.T) {
	if got := interactiveReplyID(&inboundMessage{}); got != "" {
		t.Errorf("no interactive → %q, want empty", got)
	}
	bm := &inboundMessage{Interactive: &inboundInteractive{Type: "button_reply", ButtonReply: &replyRef{ID: "opt:1"}}}
	if got := interactiveReplyID(bm); got != "opt:1" {
		t.Errorf("button_reply → %q, want opt:1", got)
	}
	lm := &inboundMessage{Interactive: &inboundInteractive{Type: "list_reply", ListReply: &replyRef{ID: "opt:5"}}}
	if got := interactiveReplyID(lm); got != "opt:5" {
		t.Errorf("list_reply → %q, want opt:5", got)
	}
}

func TestParseTextChoice(t *testing.T) {
	field := &a2ui.Field{Kind: a2ui.FieldSingleChoice, Options: []a2ui.Option{
		{Label: "Apple", Value: "apple"}, {Label: "Banana", Value: "banana"}, {Label: "Cherry", Value: "cherry"},
	}}
	opts := displayChoices(field)
	if idx, ok := parseTextChoice("2", opts); !ok || idx != 1 {
		t.Errorf("'2' → idx=%d ok=%v, want 1", idx, ok)
	}
	if idx, ok := parseTextChoice("  banana ", opts); !ok || idx != 1 {
		t.Errorf("label match → idx=%d ok=%v, want 1", idx, ok)
	}
	if _, ok := parseTextChoice("9", opts); ok {
		t.Error("out-of-range number accepted")
	}
	if _, ok := parseTextChoice("durian", opts); ok {
		t.Error("unknown label accepted")
	}
}
