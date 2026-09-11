package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

const (
	StatementSamplesPlaceholderV1  = "<!-- ALGOFORGE_SAMPLES -->"
	statementSamplesBeginPrefixV1  = "<!-- ALGOFORGE_SAMPLES_V1_BEGIN count="
	statementSamplesEndV1          = "<!-- ALGOFORGE_SAMPLES_V1_END -->"
	maxStatementSampleValueBytesV1 = 1 << 20
)

// StatementSampleV1 is the exact text pair rendered into and parsed back from
// a statement. Values are text, not normalized by this contract.
type StatementSampleV1 struct {
	Input  string `json:"input"`
	Output string `json:"output"`
}

// ValidateSampleInputProfileV1 validates the parser's deliberately narrow
// support surface without requiring a candidate. This keeps zero-sample
// statements fail closed for unsupported string/graph/tree/grid contracts.
func ValidateSampleInputProfileV1(semanticSpec domain.SemanticSpecV1) error {
	parser := &sampleInputParserV1{
		spec:     semanticSpec,
		bindings: make(map[string]*SampleInputBindingV1),
	}
	return parser.validateContract()
}

// RenderStatementSamplesV1 replaces exactly one server marker with a
// canonical fenced section. Byte length and SHA-256 headers make an embedded
// fence line unambiguous and make hand edits fail closed on parse-back.
func RenderStatementSamplesV1(draft string, samples []StatementSampleV1) (string, error) {
	if strings.Count(draft, StatementSamplesPlaceholderV1) != 1 {
		return "", fmt.Errorf("statement must contain exactly one sample placeholder")
	}
	section, err := renderStatementSampleSectionV1(samples)
	if err != nil {
		return "", err
	}
	return strings.Replace(draft, StatementSamplesPlaceholderV1, section, 1), nil
}

// ParseStatementSamplesV1 extracts the unique canonical sample section and
// rejects any formatting, ordering, length, or digest deviation.
func ParseStatementSamplesV1(markdown string) ([]StatementSampleV1, error) {
	if strings.Contains(markdown, StatementSamplesPlaceholderV1) {
		return nil, fmt.Errorf("final statement still contains the sample placeholder")
	}
	if strings.Count(markdown, statementSamplesBeginPrefixV1) != 1 ||
		strings.Count(markdown, statementSamplesEndV1) != 1 {
		return nil, fmt.Errorf("statement must contain exactly one canonical sample section")
	}
	start := strings.Index(markdown, statementSamplesBeginPrefixV1)
	endRelative := strings.Index(markdown[start:], statementSamplesEndV1)
	if endRelative < 0 {
		return nil, fmt.Errorf("sample section end marker is missing")
	}
	end := start + endRelative + len(statementSamplesEndV1)
	section := markdown[start:end]
	samples, err := parseStatementSampleSectionV1(section)
	if err != nil {
		return nil, err
	}
	canonical, err := renderStatementSampleSectionV1(samples)
	if err != nil {
		return nil, err
	}
	if canonical != section {
		return nil, fmt.Errorf("sample section is not canonical")
	}
	return samples, nil
}

func renderStatementSampleSectionV1(samples []StatementSampleV1) (string, error) {
	if len(samples) > domain.MaxGeneratedTestCases {
		return "", fmt.Errorf("sample count %d exceeds %d", len(samples), domain.MaxGeneratedTestCases)
	}
	var builder strings.Builder
	builder.WriteString(statementSamplesBeginPrefixV1)
	builder.WriteString(strconv.Itoa(len(samples)))
	builder.WriteString(" -->\n### Samples\n")
	for index, sample := range samples {
		if err := validateStatementSampleTextV1("input", sample.Input); err != nil {
			return "", fmt.Errorf("sample %d: %w", index+1, err)
		}
		if err := validateStatementSampleTextV1("output", sample.Output); err != nil {
			return "", fmt.Errorf("sample %d: %w", index+1, err)
		}
		appendStatementSampleBlockV1(&builder, "input", index+1, sample.Input)
		appendStatementSampleBlockV1(&builder, "output", index+1, sample.Output)
	}
	builder.WriteString(statementSamplesEndV1)
	return builder.String(), nil
}

func appendStatementSampleBlockV1(builder *strings.Builder, kind string, index int, value string) {
	builder.WriteString("#### ")
	builder.WriteString(kind)
	builder.WriteString(strconv.Itoa(index))
	builder.WriteString(" bytes=")
	builder.WriteString(strconv.Itoa(len(value)))
	builder.WriteString(" sha256=")
	builder.WriteString(statementSampleSHA256V1(value))
	builder.WriteString("\n```text\n")
	builder.WriteString(value)
	if !strings.HasSuffix(value, "\n") {
		builder.WriteByte('\n')
	}
	builder.WriteString("```\n")
}

func parseStatementSampleSectionV1(section string) ([]StatementSampleV1, error) {
	cursor := &statementSampleCursorV1{data: []byte(section)}
	begin, err := cursor.line()
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(begin, statementSamplesBeginPrefixV1) || !strings.HasSuffix(begin, " -->") {
		return nil, fmt.Errorf("invalid sample section begin marker")
	}
	countText := strings.TrimSuffix(strings.TrimPrefix(begin, statementSamplesBeginPrefixV1), " -->")
	if countText == "" || (len(countText) > 1 && countText[0] == '0') {
		return nil, fmt.Errorf("sample count is not canonical")
	}
	count, err := strconv.Atoi(countText)
	if err != nil || count < 0 || count > domain.MaxGeneratedTestCases {
		return nil, fmt.Errorf("invalid sample count %q", countText)
	}
	heading, err := cursor.line()
	if err != nil || heading != "### Samples" {
		return nil, fmt.Errorf("canonical Samples heading is missing")
	}
	samples := make([]StatementSampleV1, count)
	for index := 1; index <= count; index++ {
		input, err := cursor.block("input", index)
		if err != nil {
			return nil, err
		}
		output, err := cursor.block("output", index)
		if err != nil {
			return nil, err
		}
		samples[index-1] = StatementSampleV1{Input: input, Output: output}
	}
	end, err := cursor.finalLine()
	if err != nil || end != statementSamplesEndV1 || cursor.pos != len(cursor.data) {
		return nil, fmt.Errorf("invalid sample section end marker or trailing content")
	}
	return samples, nil
}

type statementSampleCursorV1 struct {
	data []byte
	pos  int
}

func (cursor *statementSampleCursorV1) line() (string, error) {
	if cursor.pos >= len(cursor.data) {
		return "", fmt.Errorf("unexpected end of sample section")
	}
	relative := strings.IndexByte(string(cursor.data[cursor.pos:]), '\n')
	if relative < 0 {
		return "", fmt.Errorf("sample section line is not LF-terminated")
	}
	line := string(cursor.data[cursor.pos : cursor.pos+relative])
	cursor.pos += relative + 1
	return line, nil
}

func (cursor *statementSampleCursorV1) finalLine() (string, error) {
	if cursor.pos >= len(cursor.data) {
		return "", fmt.Errorf("unexpected end of sample section")
	}
	line := string(cursor.data[cursor.pos:])
	cursor.pos = len(cursor.data)
	return line, nil
}

func (cursor *statementSampleCursorV1) block(kind string, expectedIndex int) (string, error) {
	header, err := cursor.line()
	if err != nil {
		return "", err
	}
	prefix := "#### " + kind + strconv.Itoa(expectedIndex) + " bytes="
	if !strings.HasPrefix(header, prefix) {
		return "", fmt.Errorf("expected %s%d block", kind, expectedIndex)
	}
	metadata := strings.TrimPrefix(header, prefix)
	parts := strings.Split(metadata, " sha256=")
	if len(parts) != 2 || parts[0] == "" || (len(parts[0]) > 1 && parts[0][0] == '0') {
		return "", fmt.Errorf("invalid %s%d byte/hash metadata", kind, expectedIndex)
	}
	size, err := strconv.Atoi(parts[0])
	if err != nil || size < 0 || size > maxStatementSampleValueBytesV1 || !isStatementSampleSHA256V1(parts[1]) {
		return "", fmt.Errorf("invalid %s%d byte/hash metadata", kind, expectedIndex)
	}
	opening, err := cursor.line()
	if err != nil || opening != "```text" {
		return "", fmt.Errorf("expected canonical text fence for %s%d", kind, expectedIndex)
	}
	if len(cursor.data)-cursor.pos < size {
		return "", fmt.Errorf("%s%d content is shorter than declared", kind, expectedIndex)
	}
	value := string(cursor.data[cursor.pos : cursor.pos+size])
	cursor.pos += size
	if !strings.HasSuffix(value, "\n") {
		if cursor.pos >= len(cursor.data) || cursor.data[cursor.pos] != '\n' {
			return "", fmt.Errorf("%s%d fence separator is missing", kind, expectedIndex)
		}
		cursor.pos++
	}
	closing, err := cursor.line()
	if err != nil || closing != "```" {
		return "", fmt.Errorf("expected canonical closing fence for %s%d", kind, expectedIndex)
	}
	if err := validateStatementSampleTextV1(kind, value); err != nil {
		return "", fmt.Errorf("%s%d: %w", kind, expectedIndex, err)
	}
	if statementSampleSHA256V1(value) != parts[1] {
		return "", fmt.Errorf("%s%d SHA-256 mismatch", kind, expectedIndex)
	}
	return value, nil
}

func validateStatementSampleTextV1(kind, value string) error {
	if len(value) > maxStatementSampleValueBytesV1 {
		return fmt.Errorf("%s exceeds %d bytes", kind, maxStatementSampleValueBytesV1)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s is not valid UTF-8", kind)
	}
	for _, character := range value {
		if character == '\r' || character == '\x00' {
			return fmt.Errorf("%s contains a forbidden character", kind)
		}
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return fmt.Errorf("%s contains forbidden control character U+%04X", kind, character)
		}
	}
	return nil
}

func statementSampleSHA256V1(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func isStatementSampleSHA256V1(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
