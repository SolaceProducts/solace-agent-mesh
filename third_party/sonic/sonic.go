package sonic

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type Config struct {
	EscapeHTML  bool
	SortMapKeys bool
	UseInt64    bool
}

type API interface {
	Marshal(v any) ([]byte, error)
	MarshalIndent(v any, prefix, indent string) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

var ConfigDefault = Config{}.Froze()

var ConfigStd = Config{EscapeHTML: true, SortMapKeys: true}.Froze()

func (cfg Config) Froze() API {
	return frozenConfig{cfg: cfg}
}

type frozenConfig struct {
	cfg Config
}

func (f frozenConfig) Marshal(v any) ([]byte, error) {
	return f.encode(v, "", "")
}

func (f frozenConfig) MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return f.encode(v, prefix, indent)
}

func (f frozenConfig) encode(v any, prefix, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(f.cfg.EscapeHTML)
	enc.SetIndent(prefix, indent)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func (f frozenConfig) Unmarshal(data []byte, v any) error {
	target, isInterface := v.(*any)
	if !f.cfg.UseInt64 || !isInterface {
		return json.Unmarshal(data, v)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errTrailingData
	}
	converted, err := numbersToInt64(decoded)
	if err != nil {
		return err
	}
	*target = converted
	return nil
}

var errTrailingData = errors.New("sonic: data after the top-level value")

func numbersToInt64(v any) (any, error) {
	switch value := v.(type) {
	case json.Number:
		if i, err := value.Int64(); err == nil {
			return i, nil
		}
		return value.Float64()
	case map[string]any:
		for key, elem := range value {
			converted, err := numbersToInt64(elem)
			if err != nil {
				return nil, err
			}
			value[key] = converted
		}
	case []any:
		for i, elem := range value {
			converted, err := numbersToInt64(elem)
			if err != nil {
				return nil, err
			}
			value[i] = converted
		}
	}
	return v, nil
}

func Marshal(v any) ([]byte, error) {
	return ConfigDefault.Marshal(v)
}

func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return ConfigDefault.MarshalIndent(v, prefix, indent)
}

func MarshalString(v any) (string, error) {
	data, err := Marshal(v)
	return string(data), err
}

func Unmarshal(data []byte, v any) error {
	return ConfigDefault.Unmarshal(data, v)
}

func UnmarshalString(data string, v any) error {
	return Unmarshal([]byte(data), v)
}

func Valid(data []byte) bool {
	return json.Valid(data)
}

type NoCopyRawMessage []byte

func (m NoCopyRawMessage) MarshalJSON() ([]byte, error) {
	if m == nil {
		return []byte("null"), nil
	}
	return m, nil
}

func (m *NoCopyRawMessage) UnmarshalJSON(data []byte) error {
	if m == nil {
		return errors.New("sonic: UnmarshalJSON on nil NoCopyRawMessage")
	}
	*m = append((*m)[:0], data...)
	return nil
}

var ErrNotExist = errors.New("sonic: value not exists")

var ErrUnsupportedType = errors.New("sonic: unsupported type")

type Node struct {
	raw []byte
}

func Get(src []byte, path ...any) (Node, error) {
	if len(path) == 0 {
		if !json.Valid(src) {
			return Node{}, errInvalidJSON
		}
		return Node{raw: bytes.TrimSpace(src)}, nil
	}
	current := src
	for _, element := range path {
		key, ok := element.(string)
		if !ok {
			return Node{}, fmt.Errorf("sonic: unsupported path element %T", element)
		}
		value, err := firstObjectField(current, key)
		if err != nil {
			return Node{}, err
		}
		current = value
	}
	return Node{raw: current}, nil
}

var (
	errInvalidJSON = errors.New("sonic: invalid JSON")
	errNotAnObject = errors.New("sonic: value is not an object")
)

func firstObjectField(src []byte, key string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(src))
	opening, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if opening != json.Delim('{') {
		return nil, errNotAnObject
	}
	for dec.More() {
		name, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		if name == key {
			return value, nil
		}
	}
	return nil, ErrNotExist
}

func GetFromString(src string, path ...any) (Node, error) {
	return Get([]byte(src), path...)
}

func (n Node) Exists() bool {
	return len(n.raw) > 0
}

func (n Node) Raw() (string, error) {
	if !n.Exists() {
		return "", ErrNotExist
	}
	return string(n.raw), nil
}

func (n Node) String() (string, error) {
	if !n.Exists() {
		return "", ErrNotExist
	}
	switch n.raw[0] {
	case '"':
		var s string
		err := json.Unmarshal(n.raw, &s)
		return s, err
	case 'n':
		return "", nil
	case '{', '[':
		return "", ErrUnsupportedType
	default:
		return string(n.raw), nil
	}
}
