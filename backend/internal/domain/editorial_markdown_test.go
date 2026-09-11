package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseEditorialMarkdownV1UnwrapsProviderEnvelopes(t *testing.T) {
	markdown := "# 奇偶回声\n\n## 方法\n\n使用 BFS。"
	object, err := json.Marshal(map[string]string{"editorial": markdown})
	if err != nil {
		t.Fatalf("marshal editorial object: %v", err)
	}
	doubleEncoded, err := json.Marshal(string(object))
	if err != nil {
		t.Fatalf("marshal nested editorial object: %v", err)
	}

	cases := map[string]string{
		"object":         string(object),
		"json string":    string(doubleEncoded),
		"fenced object":  "```json\n" + string(object) + "\n```",
		"wrapped object": `{"data":` + string(object) + `}`,
		"plain markdown": markdown,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ParseEditorialMarkdownV1(input)
			if err != nil {
				t.Fatalf("parse editorial: %v", err)
			}
			if got != markdown {
				t.Fatalf("editorial = %q, want %q", got, markdown)
			}
			if strings.HasPrefix(strings.TrimSpace(got), "{") {
				t.Fatalf("JSON envelope leaked into Markdown: %q", got[:minEditorialTestPrefix(len(got), 80)])
			}
		})
	}
}

func TestParseEditorialMarkdownV1DecodesDoubleEscapedLayoutWithoutBreakingCodeEscapes(t *testing.T) {
	input := `{"editorial":"# 标题\\n\\n说明\\n` + "```" + `cpp\\nint main(){ std::cout << '\\n'; }\\n` + "```" + `"}`
	want := "# 标题\n\n说明\n```cpp\nint main(){ std::cout << '\\n'; }\n```"

	got, err := ParseEditorialMarkdownV1(input)
	if err != nil {
		t.Fatalf("parse double-escaped editorial: %v", err)
	}
	if got != want {
		t.Fatalf("editorial = %q, want %q", got, want)
	}
}

func TestParseEditorialMarkdownV1RecoversLiteralNewlinesInHistoricalEnvelope(t *testing.T) {
	// Older rows were written with literal line breaks inside the JSON string,
	// which makes the outer document invalid strict JSON. The named-field
	// compatibility scanner must still recover the Markdown payload.
	input := "{\"editorial\":\"# 标题\n\n说明\n```cpp\nint main() { return 0; }\n```\"}"
	want := "# 标题\n\n说明\n```cpp\nint main() { return 0; }\n```"

	got, err := ParseEditorialMarkdownV1(input)
	if err != nil {
		t.Fatalf("parse historical editorial envelope: %v", err)
	}
	if got != want {
		t.Fatalf("editorial = %q, want %q", got, want)
	}
}

func TestParseEditorialMarkdownV1RejectsEmptyOrMalformedEnvelope(t *testing.T) {
	for _, input := range []string{"", `{"editorial":""}`, `{"editorial":`} {
		if got, err := ParseEditorialMarkdownV1(input); err == nil || got != "" {
			t.Fatalf("input %q returned got=%q err=%v; expected an error", input, got, err)
		}
	}
}

func minEditorialTestPrefix(length, limit int) int {
	if length < limit {
		return length
	}
	return limit
}
