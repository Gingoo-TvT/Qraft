package activities

import "strings"

// normalizeGeneratedGeneratorCode repairs formatting escapes introduced when
// an LLM serializes C++ source inside JSON.  A valid JSON string decodes its
// line breaks to real newlines, but models occasionally escape that layer one
// more time and return the two characters "\\n" between C++ statements.  Such
// a source reaches the compiler as one line and commonly fails with an
// "undefined reference to main" diagnostic.
//
// The repair is deliberately lexical: escaped line breaks are decoded only
// outside C++ string/character/raw-string literals, where a backslash-n is a
// legitimate program escape and must be preserved.  This keeps generated
// programs that print a newline byte-for-byte correct while fixing transport
// damage around declarations and statements.
func normalizeGeneratedGeneratorCode(source string) string {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	source = strings.ReplaceAll(source, "\r", "\n")
	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}
	// Decode transport escapes first so a fenced response whose line breaks
	// were also over-escaped can be unwrapped by the same deterministic path.
	source = decodeGeneratorTransportEscapes(source)
	source = stripGeneratedGeneratorFence(source)
	return strings.TrimSpace(source)
}

func stripGeneratedGeneratorFence(source string) string {
	lines := strings.Split(source, "\n")
	if len(lines) < 2 {
		return source
	}
	first := strings.TrimSpace(lines[0])
	last := strings.TrimSpace(lines[len(lines)-1])
	if (strings.HasPrefix(first, "```") && last == "```") ||
		(strings.HasPrefix(first, "~~~") && last == "~~~") {
		return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
	}
	// Be tolerant of a short accidental preamble (for example, "Here is the
	// generator:") while still requiring a matching closing fence.  We only
	// extract when a fence is present; ordinary C++ backticks are left alone.
	for start := 0; start < len(lines); start++ {
		marker := ""
		trimmed := strings.TrimSpace(lines[start])
		switch {
		case strings.HasPrefix(trimmed, "```"):
			marker = "```"
		case strings.HasPrefix(trimmed, "~~~"):
			marker = "~~~"
		default:
			continue
		}
		for end := start + 1; end < len(lines); end++ {
			if strings.TrimSpace(lines[end]) == marker {
				return strings.TrimSpace(strings.Join(lines[start+1:end], "\n"))
			}
		}
		break
	}
	return source
}

type generatorLexState uint8

const (
	generatorLexNormal generatorLexState = iota
	generatorLexString
	generatorLexCharacter
	generatorLexLineComment
	generatorLexBlockComment
	generatorLexRawString
)

func decodeGeneratorTransportEscapes(source string) string {
	var builder strings.Builder
	builder.Grow(len(source))

	state := generatorLexNormal
	quote := byte(0)
	escaped := false
	rawTerminator := ""

	for index := 0; index < len(source); {
		character := source[index]

		switch state {
		case generatorLexNormal:
			if character == '\r' {
				builder.WriteByte('\n')
				index++
				if index < len(source) && source[index] == '\n' {
					index++
				}
				continue
			}
			if character == '\n' {
				builder.WriteByte('\n')
				index++
				continue
			}
			if character == '/' && index+1 < len(source) && source[index+1] == '/' {
				builder.WriteString("//")
				index += 2
				state = generatorLexLineComment
				continue
			}
			if character == '/' && index+1 < len(source) && source[index+1] == '*' {
				builder.WriteString("/*")
				index += 2
				state = generatorLexBlockComment
				continue
			}
			if character == 'R' && index+1 < len(source) && source[index+1] == '"' {
				if terminator, end, ok := generatorRawStringStart(source, index); ok {
					builder.WriteString(source[index:end])
					index = end
					rawTerminator = terminator
					state = generatorLexRawString
					continue
				}
			}
			if character == '"' {
				builder.WriteByte(character)
				index++
				quote = character
				escaped = false
				state = generatorLexString
				continue
			}
			if character == '\'' {
				builder.WriteByte(character)
				index++
				quote = character
				escaped = false
				state = generatorLexCharacter
				continue
			}
			if consumed, ok := consumeGeneratorLayoutEscape(source, index); ok {
				builder.WriteByte('\n')
				index += consumed
				continue
			}
			builder.WriteByte(character)
			index++

		case generatorLexString, generatorLexCharacter:
			builder.WriteByte(character)
			index++
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == quote {
				state = generatorLexNormal
			}

		case generatorLexLineComment:
			if character == '\r' || character == '\n' {
				builder.WriteByte('\n')
				index++
				if character == '\r' && index < len(source) && source[index] == '\n' {
					index++
				}
				state = generatorLexNormal
				continue
			}
			if consumed, ok := consumeGeneratorLayoutEscape(source, index); ok {
				builder.WriteByte('\n')
				index += consumed
				state = generatorLexNormal
				continue
			}
			builder.WriteByte(character)
			index++

		case generatorLexBlockComment:
			if character == '*' && index+1 < len(source) && source[index+1] == '/' {
				builder.WriteString("*/")
				index += 2
				state = generatorLexNormal
				continue
			}
			if character == '\r' {
				builder.WriteByte('\n')
				index++
				if index < len(source) && source[index] == '\n' {
					index++
				}
				continue
			}
			if character == '\n' {
				builder.WriteByte('\n')
				index++
				continue
			}
			if consumed, ok := consumeGeneratorLayoutEscape(source, index); ok {
				builder.WriteByte('\n')
				index += consumed
				continue
			}
			builder.WriteByte(character)
			index++

		case generatorLexRawString:
			if rawTerminator != "" && strings.HasPrefix(source[index:], rawTerminator) {
				builder.WriteString(rawTerminator)
				index += len(rawTerminator)
				rawTerminator = ""
				state = generatorLexNormal
				continue
			}
			builder.WriteByte(character)
			index++
		}
	}

	return builder.String()
}

// consumeGeneratorLayoutEscape recognizes a transport-level escaped control
// character.  Multiple backslashes are accepted because JSON serialization
// can add more than one escaping layer.  The caller only invokes this outside
// C++ literals, so normal string/character escapes remain untouched.
func consumeGeneratorLayoutEscape(source string, start int) (int, bool) {
	if start >= len(source) || source[start] != '\\' {
		return 0, false
	}
	index := start
	for index < len(source) && source[index] == '\\' {
		index++
	}
	if index >= len(source) {
		return 0, false
	}
	switch source[index] {
	case 'n', 'r':
		index++
		if source[index-1] == 'r' {
			lineBreakStart := index
			for index < len(source) && source[index] == '\\' {
				index++
			}
			if index < len(source) && source[index] == 'n' {
				index++
			} else {
				index = lineBreakStart
			}
		}
		return index - start, true
	case 't':
		return index - start + 1, true
	default:
		return 0, false
	}
}

func generatorRawStringStart(source string, start int) (string, int, bool) {
	if start+1 >= len(source) || source[start] != 'R' || source[start+1] != '"' {
		return "", 0, false
	}
	open := start + 2
	for index := open; index < len(source) && index-open <= 16; index++ {
		if source[index] == '(' {
			delimiter := source[open:index]
			return ")" + delimiter + "\"", index + 1, true
		}
		if source[index] == '\\' || source[index] == ' ' || source[index] == '\t' || source[index] == '\r' || source[index] == '\n' {
			return "", 0, false
		}
	}
	return "", 0, false
}
