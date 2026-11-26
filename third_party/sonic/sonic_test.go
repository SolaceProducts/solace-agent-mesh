package sonic

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"unicode/utf8"
)

type marshalSample struct {
	Text string         `json:"text"`
	Map  map[string]int `json:"map"`
}

const escapedHTML = `\u003ca\u0026b\u003e`

func TestMarshal(t *testing.T) {
	t.Parallel()
	value := marshalSample{Text: "<a&b>", Map: map[string]int{"z": 1, "a": 2}}
	tests := []struct {
		name string
		api  API
		want string
	}{
		{name: "default leaves HTML unescaped and sorts keys", api: ConfigDefault, want: `{"text":"<a&b>","map":{"a":2,"z":1}}`},
		{name: "std escapes HTML", api: ConfigStd, want: `{"text":"` + escapedHTML + `","map":{"a":2,"z":1}}`},
		{name: "sort-keys config leaves HTML unescaped", api: Config{SortMapKeys: true}.Froze(), want: `{"text":"<a&b>","map":{"a":2,"z":1}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.api.Marshal(value)
			if err != nil {
				t.Fatalf("Marshal error = %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("Marshal = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestMarshalReplacesInvalidUTF8(t *testing.T) {
	t.Parallel()
	for name, api := range map[string]API{"default": ConfigDefault, "std": ConfigStd} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			encoded, err := api.Marshal("a\xffb")
			if err != nil {
				t.Fatalf("Marshal error = %v", err)
			}
			if !utf8.Valid(encoded) {
				t.Errorf("Marshal(%q) = %q, want valid UTF-8", "a\xffb", encoded)
			}
			var decoded string
			if err := Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("Unmarshal(%q) error = %v", encoded, err)
			}
			if decoded != "a\xef\xbf\xbdb" {
				t.Errorf("Marshal(%q) = %q, decodes to %q, want the invalid byte replaced by U+FFFD", "a\xffb", encoded, decoded)
			}
		})
	}
}

func TestPackageMarshalFunctionsUseDefaultConfig(t *testing.T) {
	t.Parallel()
	value := map[string]string{"b": "<x>", "a": "y"}

	got, err := Marshal(value)
	if err != nil || string(got) != `{"a":"y","b":"<x>"}` {
		t.Errorf("Marshal = %s, %v", got, err)
	}
	gotString, err := MarshalString(value)
	if err != nil || gotString != `{"a":"y","b":"<x>"}` {
		t.Errorf("MarshalString = %s, %v", gotString, err)
	}
	gotIndent, err := MarshalIndent(value, ">", "  ")
	if err != nil || string(gotIndent) != "{\n>  \"a\": \"y\",\n>  \"b\": \"<x>\"\n>}" {
		t.Errorf("MarshalIndent = %q, %v", gotIndent, err)
	}
}

func TestMarshalReturnsEncodeError(t *testing.T) {
	t.Parallel()
	var unsupported *json.UnsupportedTypeError
	if _, err := Marshal(make(chan int)); !errors.As(err, &unsupported) {
		t.Errorf("Marshal(chan) error = %v, want *json.UnsupportedTypeError", err)
	}
	if _, err := MarshalString(make(chan int)); !errors.As(err, &unsupported) {
		t.Errorf("MarshalString(chan) error = %v, want *json.UnsupportedTypeError", err)
	}
}

func TestUnmarshalReturnsDecodeError(t *testing.T) {
	t.Parallel()
	var typed struct{ N int }
	var mismatch *json.UnmarshalTypeError
	if err := Unmarshal([]byte(`{"N":"x"}`), &typed); !errors.As(err, &mismatch) {
		t.Errorf("Unmarshal type mismatch error = %v, want *json.UnmarshalTypeError", err)
	}
	if err := UnmarshalString(`{"N":"x"}`, &typed); !errors.As(err, &mismatch) {
		t.Errorf("UnmarshalString type mismatch error = %v, want *json.UnmarshalTypeError", err)
	}
	var syntax *json.SyntaxError
	if err := Unmarshal([]byte(`[1,]`), &typed); !errors.As(err, &syntax) {
		t.Errorf("Unmarshal syntax error = %v, want *json.SyntaxError", err)
	}
}

func TestUnmarshalUseInt64(t *testing.T) {
	t.Parallel()
	useInt64 := Config{UseInt64: true}.Froze()
	tests := []struct {
		name         string
		in           string
		want         any
		wantErr      bool
		wantTrailing bool
	}{
		{name: "integer becomes int64", in: `9007199254740993`, want: int64(9007199254740993)},
		{name: "fraction stays float64", in: `1.5`, want: 1.5},
		{name: "integer too large for int64 becomes float64", in: `9223372036854775808`, want: 9223372036854775808.0},
		{name: "nested integers become int64", in: `{"a":[1,{"b":2}]}`, want: map[string]any{"a": []any{int64(1), map[string]any{"b": int64(2)}}}},
		{name: "string is unchanged", in: `"7"`, want: "7"},
		{name: "trailing whitespace is accepted", in: "1 \n", want: int64(1)},
		{name: "trailing value is rejected", in: `1 2`, wantTrailing: true},
		{name: "trailing closing bracket is rejected", in: `1]`, wantTrailing: true},
		{name: "trailing closing brace is rejected", in: `{"a":1}}`, wantTrailing: true},
		{name: "number out of float64 range is rejected", in: `1e400`, wantErr: true},
		{name: "invalid JSON is rejected", in: `[1,]`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got any
			err := useInt64.Unmarshal([]byte(tc.in), &got)
			if errors.Is(err, errTrailingData) != tc.wantTrailing {
				t.Fatalf("Unmarshal(%s) error = %v, want trailing-data error %v", tc.in, err, tc.wantTrailing)
			}
			if !tc.wantTrailing && (err != nil) != tc.wantErr {
				t.Fatalf("Unmarshal(%s) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Unmarshal(%s) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestUnmarshalDefaultDecodesNumbersAsFloat64(t *testing.T) {
	t.Parallel()
	var viaBytes, viaString any
	if err := Unmarshal([]byte(`{"n":3}`), &viaBytes); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	if err := UnmarshalString(`{"n":3}`, &viaString); err != nil {
		t.Fatalf("UnmarshalString error = %v", err)
	}
	want := map[string]any{"n": 3.0}
	if !reflect.DeepEqual(viaBytes, want) || !reflect.DeepEqual(viaString, want) {
		t.Errorf("Unmarshal = %#v, UnmarshalString = %#v, want %#v", viaBytes, viaString, want)
	}
}

func TestUnmarshalUseInt64IgnoredForTypedTarget(t *testing.T) {
	t.Parallel()
	var got struct{ N any }
	if err := (Config{UseInt64: true}.Froze()).Unmarshal([]byte(`{"N":3}`), &got); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	if got.N != 3.0 {
		t.Errorf("N = %#v, want float64 3", got.N)
	}
}

func TestValid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []byte
		want bool
	}{
		{name: "complete document is valid", in: []byte(`{"a":[1]}`), want: true},
		{name: "truncated document is invalid", in: []byte(`{"a":`), want: false},
		{name: "empty input is invalid", in: []byte{}, want: false},
		{name: "nil input is invalid", in: nil, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Valid(tc.in); got != tc.want {
				t.Errorf("Valid(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestNoCopyRawMessage(t *testing.T) {
	t.Parallel()
	src := []byte(`{"response":{"x":1}}`)
	var got struct {
		Response NoCopyRawMessage `json:"response"`
	}
	if err := Unmarshal(src, &got); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	src[13] = 'y'
	if string(got.Response) != `{"x":1}` {
		t.Errorf("Response = %s after the source changed, want an independent copy {\"x\":1}", got.Response)
	}

	out, err := Marshal(struct {
		Set   NoCopyRawMessage `json:"set"`
		Unset NoCopyRawMessage `json:"unset"`
	}{Set: NoCopyRawMessage(`[1, 2]`)})
	if err != nil || string(out) != `{"set":[1,2],"unset":null}` {
		t.Errorf("Marshal = %s, %v", out, err)
	}
}

func TestGet(t *testing.T) {
	t.Parallel()
	const doc = `{"usage": {"in": 1,  "out": 2}, "name": "a\"b", "count": 42, "ok": true, "none": null, "list": [1]}`
	tests := []struct {
		name       string
		src        string
		path       []any
		wantExists bool
		wantRaw    string
		wantString string
		wantErr    error
		wantRawErr error
		wantStrErr error
	}{
		{name: "object keeps its source text", src: doc, path: []any{"usage"}, wantExists: true, wantRaw: `{"in": 1,  "out": 2}`, wantStrErr: ErrUnsupportedType},
		{name: "nested key", src: doc, path: []any{"usage", "out"}, wantExists: true, wantRaw: `2`, wantString: `2`},
		{name: "string is unquoted", src: doc, path: []any{"name"}, wantExists: true, wantRaw: `"a\"b"`, wantString: `a"b`},
		{name: "number is its text", src: doc, path: []any{"count"}, wantExists: true, wantRaw: `42`, wantString: `42`},
		{name: "boolean is its text", src: doc, path: []any{"ok"}, wantExists: true, wantRaw: `true`, wantString: `true`},
		{name: "null exists and is empty", src: doc, path: []any{"none"}, wantExists: true, wantRaw: `null`, wantString: ``},
		{name: "array is unsupported as a string", src: doc, path: []any{"list"}, wantExists: true, wantRaw: `[1]`, wantStrErr: ErrUnsupportedType},
		{name: "missing key does not exist", src: doc, path: []any{"error"}, wantErr: ErrNotExist, wantRawErr: ErrNotExist, wantStrErr: ErrNotExist},
		{name: "first duplicate key wins", src: `{"a":1,"a":2}`, path: []any{"a"}, wantExists: true, wantRaw: `1`, wantString: `1`},
		{name: "document truncated after the key still yields it", src: `{"usage":{"in":1}, "trunc": "abc`, path: []any{"usage"}, wantExists: true, wantRaw: `{"in":1}`, wantStrErr: ErrUnsupportedType},
		{name: "empty path returns the whole document", src: ` [1] `, path: nil, wantExists: true, wantRaw: `[1]`, wantStrErr: ErrUnsupportedType},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			node, err := Get([]byte(tc.src), tc.path...)
			checkNode(t, "Get", node, err, tc.wantErr, tc.wantExists, tc.wantRaw, tc.wantRawErr, tc.wantString, tc.wantStrErr)
			node, err = GetFromString(tc.src, tc.path...)
			checkNode(t, "GetFromString", node, err, tc.wantErr, tc.wantExists, tc.wantRaw, tc.wantRawErr, tc.wantString, tc.wantStrErr)
		})
	}
}

func checkNode(t *testing.T, via string, node Node, err, wantErr error, wantExists bool, wantRaw string, wantRawErr error, wantString string, wantStrErr error) {
	t.Helper()
	if !errors.Is(err, wantErr) {
		t.Fatalf("%s error = %v, want %v", via, err, wantErr)
	}
	if node.Exists() != wantExists {
		t.Errorf("%s Exists = %v, want %v", via, node.Exists(), wantExists)
	}
	raw, err := node.Raw()
	if raw != wantRaw || !errors.Is(err, wantRawErr) {
		t.Errorf("%s Raw = %q, %v; want %q, %v", via, raw, err, wantRaw, wantRawErr)
	}
	str, err := node.String()
	if str != wantString || !errors.Is(err, wantStrErr) {
		t.Errorf("%s String = %q, %v; want %q, %v", via, str, err, wantString, wantStrErr)
	}
}

func TestGetRejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		path []any
	}{
		{name: "key into an array", src: `[1]`, path: []any{"a"}},
		{name: "document truncated before the key", src: `{"other": "abc`, path: []any{"a"}},
		{name: "key into invalid JSON", src: `{"a":`, path: []any{"a"}},
		{name: "invalid JSON with an empty path", src: `{"a":`, path: nil},
		{name: "non-string path element", src: `[1]`, path: []any{0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			node, err := Get([]byte(tc.src), tc.path...)
			if err == nil || errors.Is(err, ErrNotExist) {
				t.Errorf("Get error = %v, want a non-ErrNotExist error", err)
			}
			if node.Exists() {
				t.Error("Exists = true, want false")
			}
		})
	}
}
