package domain

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ParseEditorialMarkdownV1 converts the payload returned by an editorial
// provider into the Markdown text that is stored in problems.detailed_solution.
//
// Providers occasionally return a JSON string containing a JSON object (or a
// fenced/prose-wrapped JSON object) instead of the object itself.  Treating
// that transport envelope as Markdown makes both the API and the UI display a
// large block of escaped JSON.  This parser deliberately unwraps only known
// editorial/content envelopes and leaves ordinary Markdown untouched.
func ParseEditorialMarkdownV1(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", fmt.Errorf("editorial is empty")
	}

	value, found := parseEditorialPayloadV1(text, 0)
	if !found {
		// A non-JSON response is allowed as a provider fallback.  Do not accept
		// an apparently JSON object here: if it was malformed, exposing it as
		// Markdown would recreate the original corruption.
		if looksLikeJSONDocumentV1(text) {
			return "", fmt.Errorf("editorial JSON envelope is invalid")
		}
		value = text
	}

	value = normalizeEditorialTextV1(value)
	if value == "" {
		return "", fmt.Errorf("editorial is empty")
	}
	return value, nil
}

// NormalizeEditorialMarkdownV1 is a best-effort read-path compatibility
// helper.  It is safe to use for historical rows: valid envelopes are
// unwrapped, while an already-plain or malformed value is retained (with
// transport line endings normalized) so a read never silently erases content.
func NormalizeEditorialMarkdownV1(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	if normalized, err := ParseEditorialMarkdownV1(raw); err == nil {
		return normalized
	}
	return normalizeEditorialTextV1(raw)
}

const maxEditorialPayloadDepthV1 = 8

// parseEditorialPayloadV1 returns the first known editorial payload found in
// text.  The bool distinguishes "not an envelope" from an envelope carrying
// an empty value, which is handled by the public parser.
func parseEditorialPayloadV1(text string, depth int) (string, bool) {
	if depth > maxEditorialPayloadDepthV1 {
		return "", false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", true
	}

	// A historical writer sometimes inserted literal newlines into the
	// editorial JSON string, making the outer document invalid JSON.  Recover
	// the explicitly named field with a small JSON-string scanner before trying
	// strict decoding.  This remains scoped to an "editorial" key and therefore
	// cannot reinterpret arbitrary Markdown as a transport envelope.
	if editorial, found := parseLenientEditorialFieldV1(text); found {
		return editorial, true
	}

	// Markdown providers commonly fence the JSON response.  Strip only a
	// complete fence, then parse the inner value recursively.
	if inner, ok := stripEditorialFenceV1(text); ok {
		if value, found := parseEditorialPayloadV1(inner, depth+1); found {
			return value, true
		}
		// A markdown fence around plain prose is still a useful fallback.
		if strings.TrimSpace(inner) != "" && !looksLikeJSONDocumentV1(inner) {
			return inner, true
		}
	}

	var value json.RawMessage
	if err := json.Unmarshal([]byte(text), &value); err == nil {
		if parsed, found := parseEditorialJSONValueV1(value, depth+1); found {
			return parsed, true
		}
	}

	// Handle a short explanation before/after the JSON object.  Try every
	// balanced object/array candidate so an unrelated brace in prose does not
	// prevent finding the actual envelope.
	for start := 0; start < len(text); start++ {
		if text[start] != '{' && text[start] != '[' {
			continue
		}
		candidate, end, ok := balancedJSONCandidateV1(text, start)
		if !ok {
			continue
		}
		if parsed, found := parseEditorialPayloadV1(candidate, depth+1); found {
			return parsed, true
		}
		start = end - 1
	}

	return "", false
}

func parseLenientEditorialFieldV1(text string) (string, bool) {
	for searchFrom := 0; searchFrom < len(text); {
		relative := strings.Index(text[searchFrom:], `"editorial"`)
		if relative < 0 {
			return "", false
		}
		keyStart := searchFrom + relative
		colon := keyStart + len(`"editorial"`)
		for colon < len(text) && (text[colon] == ' ' || text[colon] == '\t' || text[colon] == '\r' || text[colon] == '\n') {
			colon++
		}
		if colon >= len(text) || text[colon] != ':' {
			searchFrom = keyStart + 1
			continue
		}
		colon++
		for colon < len(text) && (text[colon] == ' ' || text[colon] == '\t' || text[colon] == '\r' || text[colon] == '\n') {
			colon++
		}
		if colon >= len(text) || text[colon] != '"' {
			searchFrom = keyStart + 1
			continue
		}
		value, end, ok := decodeLenientJSONStringV1(text, colon)
		if ok {
			suffix := strings.TrimSpace(text[end:])
			if suffix == "" || strings.HasPrefix(suffix, "}") || strings.HasPrefix(suffix, "]") || strings.HasPrefix(suffix, ",") {
				return value, true
			}
		}
		searchFrom = keyStart + 1
	}
	return "", false
}

// decodeLenientJSONStringV1 decodes a JSON string while accepting literal
// control characters.  It is intentionally not a general JSON parser: the
// caller has already located the known editorial field and validates the
// trailing envelope shape.
func decodeLenientJSONStringV1(text string, quoteStart int) (string, int, bool) {
	if quoteStart < 0 || quoteStart >= len(text) || text[quoteStart] != '"' {
		return "", quoteStart, false
	}
	var builder strings.Builder
	for index := quoteStart + 1; index < len(text); index++ {
		ch := text[index]
		if ch == '"' {
			return builder.String(), index + 1, true
		}
		if ch != '\\' {
			builder.WriteByte(ch)
			continue
		}
		if index+1 >= len(text) {
			return "", index, false
		}
		index++
		switch text[index] {
		case '"':
			builder.WriteByte('"')
		case '\\':
			builder.WriteByte('\\')
		case '/':
			builder.WriteByte('/')
		case 'b':
			builder.WriteByte('\b')
		case 'f':
			builder.WriteByte('\f')
		case 'n':
			builder.WriteByte('\n')
		case 'r':
			builder.WriteByte('\r')
		case 't':
			builder.WriteByte('\t')
		case 'u':
			if index+4 >= len(text) {
				return "", index, false
			}
			digits := text[index+1 : index+5]
			codePoint, err := strconv.ParseUint(digits, 16, 16)
			if err != nil {
				return "", index, false
			}
			builder.WriteRune(rune(codePoint))
			index += 4
		default:
			// Keep unknown escapes losslessly; a Markdown/code sample may use
			// a backslash sequence that is not part of JSON's escape alphabet.
			builder.WriteByte('\\')
			builder.WriteByte(text[index])
		}
	}
	return "", len(text), false
}

func parseEditorialJSONValueV1(value json.RawMessage, depth int) (string, bool) {
	if len(value) == 0 || depth > maxEditorialPayloadDepthV1 {
		return "", false
	}

	switch value[0] {
	case '"':
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return "", false
		}
		// A JSON string may itself contain the provider's JSON object.  Recurse
		// only when it actually contains a known envelope; ordinary Markdown
		// strings are returned unchanged.
		if nested, found := parseEditorialPayloadV1(text, depth+1); found && looksLikeEditorialEnvelopeV1(text) {
			return nested, true
		}
		return text, true

	case '{':
		var object map[string]json.RawMessage
		if err := json.Unmarshal(value, &object); err != nil {
			return "", false
		}

		// Prefer the semantic editorial field over generic provider wrappers.
		for key, payload := range object {
			if strings.EqualFold(strings.TrimSpace(key), "editorial") {
				return parseEditorialFieldV1(payload, depth+1)
			}
		}
		for _, key := range []string{"markdown", "body", "content", "text", "data", "result", "output", "response"} {
			for objectKey, payload := range object {
				if strings.EqualFold(strings.TrimSpace(objectKey), key) {
					if parsed, found := parseEditorialFieldV1(payload, depth+1); found {
						return parsed, true
					}
				}
			}
		}
		return "", false

	case '[':
		// Some gateways wrap the provider response in a one-element array.
		var values []json.RawMessage
		if err := json.Unmarshal(value, &values); err != nil {
			return "", false
		}
		for _, item := range values {
			if parsed, found := parseEditorialJSONValueV1(item, depth+1); found {
				return parsed, true
			}
		}
	}

	return "", false
}

func parseEditorialFieldV1(value json.RawMessage, depth int) (string, bool) {
	if len(value) == 0 || string(value) == "null" {
		return "", true
	}
	if value[0] == '"' {
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return "", false
		}
		if nested, found := parseEditorialPayloadV1(text, depth+1); found && looksLikeEditorialEnvelopeV1(text) {
			return nested, true
		}
		return text, true
	}
	return parseEditorialJSONValueV1(value, depth+1)
}

func looksLikeEditorialEnvelopeV1(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || (text[0] != '{' && text[0] != '[' && text[0] != '`') {
		return false
	}
	for start := 0; start < len(text); start++ {
		if text[start] != '{' && text[start] != '[' {
			continue
		}
		candidate, end, ok := balancedJSONCandidateV1(text, start)
		if !ok {
			continue
		}
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(candidate), &object) == nil {
			for key := range object {
				if strings.EqualFold(strings.TrimSpace(key), "editorial") {
					return true
				}
			}
		}
		start = end - 1
	}
	return false
}

func looksLikeJSONDocumentV1(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if text[0] == '{' || text[0] == '[' || text[0] == '"' {
		return true
	}
	return strings.HasPrefix(text, "```") && strings.Contains(text, "{")
}

func stripEditorialFenceV1(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "```") {
		return "", false
	}
	firstLineEnd := strings.IndexByte(text, '\n')
	if firstLineEnd < 0 {
		// Also support a compact one-line form: ```json {...}```.
		if end := strings.LastIndex(text[3:], "```"); end >= 0 {
			inner := text[3 : 3+end]
			inner = strings.TrimSpace(strings.TrimPrefix(inner, "json"))
			return inner, true
		}
		return "", false
	}
	lastFence := strings.LastIndex(text[firstLineEnd+1:], "```")
	if lastFence < 0 {
		return "", false
	}
	lastFence += firstLineEnd + 1
	return strings.TrimSpace(text[firstLineEnd+1 : lastFence]), true
}

func balancedJSONCandidateV1(text string, start int) (string, int, bool) {
	if start < 0 || start >= len(text) || (text[start] != '{' && text[start] != '[') {
		return "", start, false
	}
	stack := make([]byte, 0, 4)
	inString := false
	escaped := false
	for index := start; index < len(text); index++ {
		ch := text[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, ch)
		case '}', ']':
			if len(stack) == 0 || (ch == '}' && stack[len(stack)-1] != '{') || (ch == ']' && stack[len(stack)-1] != '[') {
				return "", index, false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				candidate := text[start : index+1]
				if json.Valid([]byte(candidate)) {
					return candidate, index + 1, true
				}
				return "", index + 1, false
			}
		}
	}
	return "", len(text), false
}

func normalizeEditorialTextV1(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = decodeEditorialLayoutEscapesV1(text)
	return strings.TrimSpace(text)
}

// decodeEditorialLayoutEscapesV1 handles one remaining transport-encoding
// layer while preserving common source-code escapes such as '\\n' and
// "\\n".  Correctly decoded JSON already contains real newlines, so this is
// only a compatibility path for double-encoded historical/provider output.
func decodeEditorialLayoutEscapesV1(text string) string {
	if !strings.Contains(text, `\n`) && !strings.Contains(text, `\r\n`) {
		return text
	}
	var builder strings.Builder
	builder.Grow(len(text))
	for index := 0; index < len(text); {
		if index+3 < len(text) && text[index] == '\\' && text[index+1] == 'r' && text[index+2] == '\\' && text[index+3] == 'n' && editorialLayoutEscapeV1(text, index, 4) {
			builder.WriteByte('\n')
			index += 4
			continue
		}
		if index+1 < len(text) && text[index] == '\\' && text[index+1] == 'n' && editorialLayoutEscapeV1(text, index, 2) {
			builder.WriteByte('\n')
			index += 2
			continue
		}
		_, size := utf8.DecodeRuneInString(text[index:])
		if size <= 0 {
			size = 1
		}
		builder.WriteString(text[index : index+size])
		index += size
	}
	return builder.String()
}

func editorialLayoutEscapeV1(text string, slashIndex, escapeWidth int) bool {
	if slashIndex > 0 && text[slashIndex-1] == '\\' {
		return false
	}
	next := slashIndex + escapeWidth
	if next >= len(text) {
		return true
	}
	if text[next] == '\\' || text[next] == '#' || text[next] == '-' || text[next] == '*' || text[next] == '`' || text[next] == '~' || text[next] == '>' || text[next] == '|' {
		return true
	}
	if text[next] >= '0' && text[next] <= '9' {
		return true
	}
	if text[next] == '\'' || text[next] == '"' {
		return false
	}
	runeValue, _ := utf8.DecodeRuneInString(text[next:])
	if runeValue != utf8.RuneError && runeValue >= utf8.RuneSelf {
		return true
	}
	if text[next] >= 'A' && text[next] <= 'Z' || text[next] >= 'a' && text[next] <= 'z' {
		// Preserve TeX commands whose names begin with n (\\nabla, \\neq,
		// \\notin, ...).  Other letter continuations are overwhelmingly a
		// serialized line break such as "\\nint" after a fenced code label.
		end := next
		for end < len(text) && ((text[end] >= 'A' && text[end] <= 'Z') || (text[end] >= 'a' && text[end] <= 'z')) {
			end++
		}
		switch strings.ToLower(text[next:end]) {
		case "nabla", "ne", "neq", "ni", "not", "notin", "nu":
			return false
		default:
			return true
		}
	}
	return true
}
