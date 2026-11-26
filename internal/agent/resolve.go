// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/expr-lang/expr"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embed/directive"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

var ErrStatusUpdateSignal = fmt.Errorf("status_update signal")

type EmbedResolver struct {
	artifacts    *artifact.ScopedService
	agentName    string
	userID       string
	sessionID    string
	userTimezone string
	logger       *slog.Logger
}

func NewEmbedResolver(arts *artifact.ScopedService, agentName, userID, sessionID, userTimezone string, logger *slog.Logger) *EmbedResolver {
	return &EmbedResolver{
		artifacts:    arts,
		agentName:    agentName,
		userID:       userID,
		sessionID:    sessionID,
		userTimezone: userTimezone,
		logger:       logger,
	}
}

func (r *EmbedResolver) Resolve(ctx context.Context, ref *EmbedRef) (string, error) {
	switch ref.Type {
	case "math":
		return r.resolveMath(ref)
	case "datetime":
		return r.resolveDatetime(ref)
	case "uuid":
		return r.resolveUUID(ref)
	case "artifact_meta":
		return r.resolveArtifactMeta(ctx, ref)
	case "status_update":
		return r.resolveStatusUpdate(ref)
	default:
		return "", fmt.Errorf("unknown early embed type: %s", ref.Type)
	}
}

func (r *EmbedResolver) resolveMath(ref *EmbedRef) (string, error) {
	value := ref.Params["value"]
	if value == "" {
		return "None", nil
	}

	if containsTemplateSyntax(value) {
		return ref.Raw, nil
	}

	return ResolveMathExpression(value)
}

func ResolveMathExpression(value string) (string, error) {
	expression, formatSpec := splitFormatSpec(value)

	expression = strings.TrimSpace(expression)
	if expression == "" {
		return "None", nil
	}

	expression = convertPythonTupleToArray(expression)

	env := mathEnv()

	program, err := expr.Compile(expression,
		expr.Env(env),
		expr.Operator("/", "floatDivII", "floatDivIF", "floatDivFI", "floatDivFF"),
	)
	if err != nil {
		return formatMathError(expression, err), nil
	}

	result, err := expr.Run(program, env)
	if err != nil {
		return formatMathError(expression, err), nil
	}

	formatted, fmtErr := formatNumber(result, formatSpec)
	if fmtErr != nil {
		return formatMathError(expression, fmtErr), nil
	}
	return formatted, nil
}

func formatMathError(expression string, err error) string {
	msg := err.Error()
	if strings.HasPrefix(msg, "unknown name ") {
		name := strings.TrimPrefix(msg, "unknown name ")
		if idx := strings.Index(name, " ("); idx != -1 {
			name = name[:idx]
		}
		msg = fmt.Sprintf("name '%s' is not defined", name)
	}
	return fmt.Sprintf("[Error: Math evaluation error: %s]", msg)
}

func (r *EmbedResolver) resolveDatetime(ref *EmbedRef) (string, error) {
	return ResolveDatetimeExpression(ref.Params["value"], r.userTimezone)
}

const pythonISOFormatLayout = "2006-01-02T15:04:05.000000-07:00"

func ResolveDatetimeExpression(value, tzName string) (string, error) {
	return resolveDatetimeAt(time.Now(), value, tzName)
}

func resolveDatetimeAt(now time.Time, value, tzName string) (string, error) {
	value = strings.TrimSpace(value)
	loc, _, _ := userLocation(tzName)
	now = now.In(loc)

	switch strings.ToLower(value) {
	case "", "now", "iso":
		return now.Format(pythonISOFormatLayout), nil
	case "timestamp":
		return strconv.FormatInt(now.Unix(), 10), nil
	case "date":
		return now.Format("2006-01-02"), nil
	case "time":
		return now.Format("15:04:05"), nil
	default:
		layout := convertStrftimeToGo(value)
		return now.Format(layout), nil
	}
}

func (r *EmbedResolver) resolveUUID(_ *EmbedRef) (string, error) {
	return ResolveUUIDExpression(), nil
}

func ResolveUUIDExpression() string {
	return idgen.New()
}

func containsTemplateSyntax(s string) bool {
	return strings.Contains(s, "{{") || strings.Contains(s, "{%")
}

const artifactMetaSizeKey = "size"

func (r *EmbedResolver) resolveArtifactMeta(ctx context.Context, ref *EmbedRef) (string, error) {
	if r.artifacts == nil {
		return "[Error: ArtifactService not available in context for artifact_meta]", nil
	}

	value := ref.Params["value"]
	if value == "" {
		return "[Error: Filename missing for artifact_meta]", nil
	}

	filename, version := ParseArtifactSpec(value)
	loc := r.artifacts.Resolve(r.agentName, r.userID, r.sessionID, filename)

	var versionToLoad int
	if version != nil {
		versionToLoad = *version
	} else {
		versions, err := r.artifacts.Inner.ListVersions(ctx, loc)
		if err != nil || len(versions) == 0 {
			return fmt.Sprintf("[Error: Artifact '%s' not found (no versions available)]", filename), nil
		}
		versionToLoad = versions[len(versions)-1]
	}

	vPtr := &versionToLoad
	content, mimeType, err := r.artifacts.Inner.Load(ctx, loc, vPtr)
	if err != nil || content == nil {
		return fmt.Sprintf("[Error: Data artifact '%s' v%d not found or empty]", filename, versionToLoad), nil
	}

	metaDict := artifact.LoadCompanionMetadata(ctx, r.artifacts,
		r.agentName, r.userID, r.sessionID, filename, vPtr)

	if metaDict == nil {
		meta, _ := r.artifacts.Inner.GetVersionMetadata(ctx, loc, vPtr)
		metaDict = map[string]any{
			"filename":          filename,
			"mime_type":         mimeType,
			artifactMetaSizeKey: len(content),
		}
		if meta != nil {
			if meta.Description != "" {
				metaDict["description"] = meta.Description
			}
			for k, v := range meta.Custom {
				metaDict[k] = v
			}
		}
	}

	metaDict["version"] = versionToLoad

	return artifact.FormatMetadataForLLM(metaDict), nil
}

func (r *EmbedResolver) resolveStatusUpdate(ref *EmbedRef) (string, error) {
	value := ref.Params["value"]
	if value == "" {
		return "", ErrStatusUpdateSignal
	}
	return value, ErrStatusUpdateSignal
}

var pythonTuplePattern = regexp.MustCompile(`(\w+)\(\(([^)]*)\)\)`)

func convertPythonTupleToArray(exprStr string) string {
	return pythonTuplePattern.ReplaceAllString(exprStr, "${1}([${2}])")
}

func splitFormatSpec(s string) (expression, formatSpec string) {
	s = strings.ReplaceAll(s, ` \| `, " | ")
	idx := strings.LastIndex(s, " | ")
	if idx == -1 {
		return s, ""
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+3:])
}

func formatNumber(result any, formatSpec string) (string, error) {
	if formatSpec == "" {
		switch v := result.(type) {
		case float64:
			if math.IsInf(v, 1) {
				return "inf", nil
			}
			if math.IsInf(v, -1) {
				return "-inf", nil
			}
			if math.IsNaN(v) {
				return "nan", nil
			}
			if v == math.Trunc(v) {
				return fmt.Sprintf("%.1f", v), nil
			}
			return strconv.FormatFloat(v, 'f', -1, 64), nil
		case float32:
			return formatNumber(float64(v), formatSpec)
		case int:
			return strconv.Itoa(v), nil
		case int64:
			return strconv.FormatInt(v, 10), nil
		case bool:
			return strconv.FormatBool(v), nil
		default:
			return fmt.Sprintf("%v", v), nil
		}
	}

	spec := formatSpec
	useThousandSep := false

	if strings.HasPrefix(spec, ",") {
		useThousandSep = true
		spec = spec[1:]
	}

	if spec == "" {
		spec = "%f"
	} else if strings.HasPrefix(spec, ".") {
		spec = "%" + spec
	} else if !strings.HasPrefix(spec, "%") {
		spec = "%" + spec
	}

	if !isValidFloatFormatSpec(spec) {
		return formatNumber(result, "")
	}

	var formatted string
	switch v := result.(type) {
	case float64:
		formatted = fmt.Sprintf(spec, v)
	case float32:
		formatted = fmt.Sprintf(spec, float64(v))
	case int:
		formatted = fmt.Sprintf(spec, float64(v))
	case int64:
		formatted = fmt.Sprintf(spec, float64(v))
	default:
		return fmt.Sprintf("%v", v), nil
	}

	if useThousandSep {
		formatted = addThousandSeparators(formatted)
	}
	return formatted, nil
}

func addThousandSeparators(s string) string {
	intPart, decPart := s, ""
	if dotIdx := strings.Index(s, "."); dotIdx != -1 {
		intPart = s[:dotIdx]
		decPart = s[dotIdx:]
	}

	prefix := ""
	if strings.HasPrefix(intPart, "-") {
		prefix = "-"
		intPart = intPart[1:]
	}

	n := len(intPart)
	if n <= 3 {
		return prefix + intPart + decPart
	}

	var buf strings.Builder
	buf.Grow(n + n/3 + len(prefix) + len(decPart))
	buf.WriteString(prefix)

	firstGroup := n % 3
	if firstGroup == 0 {
		firstGroup = 3
	}
	buf.WriteString(intPart[:firstGroup])
	for i := firstGroup; i < n; i += 3 {
		buf.WriteByte(',')
		buf.WriteString(intPart[i : i+3])
	}
	buf.WriteString(decPart)
	return buf.String()
}

func mathEnv() map[string]any {
	return map[string]any{
		"pi":  math.Pi,
		"e":   math.E,
		"inf": math.Inf(1),

		"abs":   math.Abs,
		"ceil":  math.Ceil,
		"floor": math.Floor,

		"round": func(args ...any) float64 {
			if len(args) == 0 {
				return 0
			}
			val := toFloat64(args[0])
			if len(args) >= 2 {
				ndigits := toFloat64(args[1])
				factor := math.Pow(10, ndigits)
				return math.Round(val*factor) / factor
			}
			return math.Round(val)
		},

		"min": func(args ...any) float64 {
			if len(args) == 0 {
				return 0
			}
			result := toFloat64(args[0])
			for _, a := range args[1:] {
				v := toFloat64(a)
				if v < result {
					result = v
				}
			}
			return result
		},
		"max": func(args ...any) float64 {
			if len(args) == 0 {
				return 0
			}
			result := toFloat64(args[0])
			for _, a := range args[1:] {
				v := toFloat64(a)
				if v > result {
					result = v
				}
			}
			return result
		},

		"sum": func(items any) float64 {
			var result float64
			switch v := items.(type) {
			case []any:
				for _, a := range v {
					result += toFloat64(a)
				}
			case []int:
				for _, a := range v {
					result += float64(a)
				}
			case []float64:
				for _, a := range v {
					result += a
				}
			}
			return result
		},

		"sqrt":  math.Sqrt,
		"pow":   math.Pow,
		"exp":   math.Exp,
		"log":   math.Log,
		"log10": math.Log10,

		"sin":     math.Sin,
		"cos":     math.Cos,
		"tan":     math.Tan,
		"sinh":    math.Sinh,
		"cosh":    math.Cosh,
		"tanh":    math.Tanh,
		"radians": func(deg float64) float64 { return deg * math.Pi / 180 },

		"factorial": func(n int) int {
			if n < 0 {
				return 0
			}
			result := 1
			for i := 2; i <= n; i++ {
				result *= i
			}
			return result
		},

		"random":  rand.Float64,
		"randint": func(a, b int) int { return rand.IntN(b-a+1) + a },
		"uniform": func(a, b float64) float64 { return a + rand.Float64()*(b-a) },

		"floatDivII": func(a, b int) float64 { return float64(a) / float64(b) },
		"floatDivIF": func(a int, b float64) float64 { return float64(a) / b },
		"floatDivFI": func(a float64, b int) float64 { return a / float64(b) },
		"floatDivFF": func(a, b float64) float64 { return a / b },
	}
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	default:
		return 0
	}
}

func ParseArtifactSpec(spec string) (filename string, version *int) {
	return directive.ParseArtifactSpec(spec)
}

var validFloatSpecPattern = regexp.MustCompile(`^%[0-9]*\.?[0-9]*[feEgG]$`)

func isValidFloatFormatSpec(spec string) bool {
	return validFloatSpecPattern.MatchString(spec)
}

func convertStrftimeToGo(format string) string {
	replacer := strings.NewReplacer(
		"%Y", "2006",
		"%m", "01",
		"%d", "02",
		"%H", "15",
		"%M", "04",
		"%S", "05",
		"%f", "000000",
		"%z", "-0700",
		"%Z", "MST",
		"%p", "PM",
		"%I", "03",
		"%B", "January",
		"%b", "Jan",
		"%A", "Monday",
		"%a", "Mon",
	)
	return replacer.Replace(format)
}

func (r *EmbedResolver) ResolveEmbedsInString(ctx context.Context, s string) string {
	if !strings.Contains(s, EmbedOpen) {
		return s
	}

	closeLen := len(EmbedClose)
	var result strings.Builder
	remaining := s

	for {
		startIdx := strings.Index(remaining, EmbedOpen)
		if startIdx == -1 {
			result.WriteString(remaining)
			break
		}

		result.WriteString(remaining[:startIdx])

		endIdx := strings.Index(remaining[startIdx:], EmbedClose)
		if endIdx == -1 {
			result.WriteString(remaining[startIdx:])
			break
		}
		endIdx += startIdx + closeLen

		embedRaw := remaining[startIdx:endIdx]
		remaining = remaining[endIdx:]

		ref := ParseEmbed(embedRaw)
		if ref == nil || !ref.IsEarly {
			result.WriteString(embedRaw)
			continue
		}

		resolved, err := r.Resolve(ctx, ref)
		if err != nil {
			if err == ErrStatusUpdateSignal {
				continue
			}
			r.logger.Debug("embed resolution failed in string", "raw", embedRaw, "error", err)
			result.WriteString(embedRaw)
			continue
		}

		result.WriteString(resolved)
	}

	return result.String()
}

func (r *EmbedResolver) ResolveToolArgs(ctx context.Context, args map[string]any, rawStringArgs []string) map[string]any {
	if len(args) == 0 {
		return args
	}

	rawSet := make(map[string]bool, len(rawStringArgs))
	for _, k := range rawStringArgs {
		rawSet[k] = true
	}

	result := make(map[string]any, len(args))
	for k, v := range args {
		if rawSet[k] {
			result[k] = v
			continue
		}
		result[k] = r.resolveValue(ctx, v)
	}
	return result
}

func (r *EmbedResolver) resolveValue(ctx context.Context, v any) any {
	switch val := v.(type) {
	case string:
		return r.ResolveEmbedsInString(ctx, val)
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, inner := range val {
			result[k] = r.resolveValue(ctx, inner)
		}
		return result
	case []any:
		result := make([]any, len(val))
		for i, inner := range val {
			result[i] = r.resolveValue(ctx, inner)
		}
		return result
	default:
		return v
	}
}
