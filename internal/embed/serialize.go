// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

func SerializeData(data any, format DataFormat, targetFormat string, mimeType string) (string, error) {
	if targetFormat == "" {
		targetFormat = "text"
	}

	switch strings.ToLower(targetFormat) {
	case "text":
		return serializeToText(data, format, mimeType)
	case "json":
		return serializeToJSON(data, format, false)
	case "json_pretty":
		return serializeToJSON(data, format, true)
	case "csv":
		return serializeToCSV(data, format, mimeType)
	case "datauri":
		return serializeToDataURI(data, format, mimeType)
	default:
		if isNumericFormatSpec(targetFormat) {
			return serializeNumeric(data, targetFormat)
		}
		return serializeToText(data, format, mimeType)
	}
}

func serializeToText(data any, format DataFormat, mimeType string) (string, error) {
	if format == FormatString {
		s, ok := data.(string)
		if ok {
			return s, nil
		}
	}

	converted, _, err := ConvertData(data, format, FormatString, mimeType)
	if err != nil {
		return fmt.Sprintf("[Serialization Error: %s]", err), err
	}
	s, ok := converted.(string)
	if !ok {
		return fmt.Sprintf("%v", converted), nil
	}
	return s, nil
}

func serializeToJSON(data any, format DataFormat, pretty bool) (string, error) {
	jsonData := data
	if format != FormatJSON {
		converted, _, err := ConvertData(data, format, FormatJSON, "")
		if err != nil {
			return fmt.Sprintf("[Serialization Error: %s]", err), err
		}
		jsonData = converted
	}

	var b []byte
	var err error
	if pretty {
		b, err = json.MarshalIndent(jsonData, "", "  ")
	} else {
		b, err = json.Marshal(jsonData)
	}
	if err != nil {
		return fmt.Sprintf("[Serialization Error: %s]", err), err
	}
	return string(b), nil
}

func serializeToCSV(data any, format DataFormat, mimeType string) (string, error) {
	lodData := data
	if format != FormatListOfDicts {
		converted, _, err := ConvertData(data, format, FormatListOfDicts, mimeType)
		if err != nil {
			return fmt.Sprintf("[Serialization Error: %s]", err), err
		}
		lodData = converted
	}

	converted, _, err := ConvertData(lodData, FormatListOfDicts, FormatString, "text/csv")
	if err != nil {
		return fmt.Sprintf("[Serialization Error: %s]", err), err
	}
	s, ok := converted.(string)
	if !ok {
		return fmt.Sprintf("[Serialization Error: expected string from CSV conversion, got %T]", converted), fmt.Errorf("CSV conversion returned %T", converted)
	}
	return s, nil
}

func serializeToDataURI(data any, format DataFormat, mimeType string) (string, error) {
	if mimeType == "" {
		return "[Serialization Error: datauri format requires a MIME type]", fmt.Errorf("datauri format requires a MIME type")
	}

	bytesData := data
	if format != FormatBytes {
		converted, _, err := ConvertData(data, format, FormatBytes, mimeType)
		if err != nil {
			return fmt.Sprintf("[Serialization Error: %s]", err), err
		}
		bytesData = converted
	}

	b, ok := bytesData.([]byte)
	if !ok {
		return fmt.Sprintf("[Serialization Error: expected []byte, got %T]", bytesData), fmt.Errorf("expected []byte, got %T", bytesData)
	}

	encoded := base64.StdEncoding.EncodeToString(b)
	return fmt.Sprintf("data:%s;base64,%s", mimeType, encoded), nil
}

var numericFormatPattern = regexp.MustCompile(`^\.?\d*[defgEG%]$`)

func isNumericFormatSpec(spec string) bool {
	return numericFormatPattern.MatchString(spec)
}

func serializeNumeric(data any, spec string) (string, error) {
	var val float64
	switch v := data.(type) {
	case float64:
		val = v
	case float32:
		val = float64(v)
	case int:
		val = float64(v)
	case int64:
		val = float64(v)
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return fmt.Sprintf("[Serialization Error: %s]", err), err
		}
		val = f
	default:
		return fmt.Sprintf("[Serialization Error: cannot format %T with spec %q]", data, spec), fmt.Errorf("cannot format %T with spec %q", data, spec)
	}

	goSpec := pythonSpecToGo(spec)
	if goSpec == "" {
		return defaultNumericString(val), nil
	}

	if strings.HasSuffix(spec, "%") {
		val *= 100
	}

	result := fmt.Sprintf(goSpec, val)

	if strings.HasSuffix(spec, "%") && !strings.HasSuffix(result, "%") {
		result += "%"
	}

	return result, nil
}

func pythonSpecToGo(spec string) string {
	if spec == "" {
		return ""
	}

	if strings.HasSuffix(spec, "%") {
		inner := spec[:len(spec)-1]
		if inner == "" {
			return "%f"
		}
		if strings.HasPrefix(inner, ".") {
			return "%" + inner + "f"
		}
		return "%" + inner + "f"
	}

	if strings.HasPrefix(spec, ".") {
		return "%" + spec
	}

	if len(spec) == 1 {
		switch spec {
		case "d":
			return "%.0f"
		case "e", "E", "f", "g", "G":
			return "%" + spec
		}
	}

	return ""
}

func defaultNumericString(val float64) string {
	if math.IsInf(val, 1) {
		return "inf"
	}
	if math.IsInf(val, -1) {
		return "-inf"
	}
	if math.IsNaN(val) {
		return "nan"
	}
	if val == math.Trunc(val) {
		return strconv.FormatFloat(val, 'f', 1, 64)
	}
	return strconv.FormatFloat(val, 'f', -1, 64)
}
