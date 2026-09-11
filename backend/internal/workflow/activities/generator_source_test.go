package activities

import (
	"strings"
	"testing"
)

func TestNormalizeGeneratedGeneratorCodeRepairsEscapedLineBreaks(t *testing.T) {
	input := `#include <bits/stdc++.h>\nusing namespace std;\n\nint main() {\n  long long test_index, group_id, seed;\n  cin >> test_index >> group_id >> seed;\n  cout << test_index << '\\n';\n}`

	got := normalizeGeneratedGeneratorCode(input)
	if strings.Contains(got, `\nusing`) || strings.Contains(got, `\n\nint`) {
		t.Fatalf("layout escape was not decoded: %q", got)
	}
	if !strings.Contains(got, "#include <bits/stdc++.h>\nusing namespace std;") {
		t.Fatalf("missing repaired source line break: %q", got)
	}
	if !strings.Contains(got, `cout << test_index << '\\n';`) {
		t.Fatalf("C++ newline escape inside character literal was changed: %q", got)
	}
}

func TestParseTestDataResponseNormalizesOverEscapedGeneratorSource(t *testing.T) {
	response := `{"test_cases":[{"input":"","group_id":1,"is_sample":false,"description":"generated"}],"generator_code":"#include <iostream>\\nint main(){long long i,g,s;std::cin>>i>>g>>s;std::cout<<i<<'\\\\n';}"}`
	parsed, err := parseTestDataResponse(response)
	if err != nil {
		t.Fatalf("parse test data response: %v", err)
	}
	if strings.Contains(parsed.GeneratorCode, `\\nint main`) {
		t.Fatalf("parser left a transport line-break escape in source: %q", parsed.GeneratorCode)
	}
	if !strings.Contains(parsed.GeneratorCode, "#include <iostream>\nint main()") {
		t.Fatalf("parser did not restore source line break: %q", parsed.GeneratorCode)
	}
	if !strings.Contains(parsed.GeneratorCode, `<<'\\n';`) {
		t.Fatalf("parser changed C++ character escape: %q", parsed.GeneratorCode)
	}
}

func TestNormalizeGeneratedGeneratorCodePreservesOneLineLiteralEscapes(t *testing.T) {
	input := `#include <iostream>\nint main(){std::cout << "literal \\n text" << '\\n';}`
	got := normalizeGeneratedGeneratorCode(input)
	if !strings.Contains(got, "#include <iostream>\nint main()") {
		t.Fatalf("source line break was not repaired: %q", got)
	}
	if !strings.Contains(got, `"literal \\n text"`) {
		t.Fatalf("string literal escape was changed: %q", got)
	}
	if !strings.Contains(got, `'\\n'`) {
		t.Fatalf("character literal escape was changed: %q", got)
	}
}

func TestNormalizeGeneratedGeneratorCodeStripsFenceAndCRLF(t *testing.T) {
	input := "```cpp\r\n#include <iostream>\\r\\nint main(){return 0;}\r\n```"
	got := normalizeGeneratedGeneratorCode(input)
	want := "#include <iostream>\nint main(){return 0;}"
	if got != want {
		t.Fatalf("normalized fenced source = %q, want %q", got, want)
	}
}

func TestNormalizeGeneratedGeneratorCodeStripsOverEscapedFence(t *testing.T) {
	input := "```cpp\\n#include <iostream>\\nint main(){return 0;}\\n```"
	got := normalizeGeneratedGeneratorCode(input)
	want := "#include <iostream>\nint main(){return 0;}"
	if got != want {
		t.Fatalf("normalized over-escaped fenced source = %q, want %q", got, want)
	}
}

func TestNormalizeGeneratedGeneratorCodeExtractsFencedPreamble(t *testing.T) {
	input := "Here is the generator:\n```cpp\n#include <iostream>\\nint main(){return 0;}\n```\n"
	got := normalizeGeneratedGeneratorCode(input)
	want := "#include <iostream>\nint main(){return 0;}"
	if got != want {
		t.Fatalf("normalized fenced preamble = %q, want %q", got, want)
	}
}

func TestNormalizeGeneratedGeneratorCodePreservesRawStringContents(t *testing.T) {
	input := `#include <string>\nint main(){auto s=R"TAG(\nnot a source line\n)TAG"; return s.empty();}`
	got := normalizeGeneratedGeneratorCode(input)
	if !strings.Contains(got, "R\"TAG(\\nnot a source line\\n)TAG\"") {
		t.Fatalf("raw string contents were changed: %q", got)
	}
	if !strings.Contains(got, "#include <string>\nint main()") {
		t.Fatalf("source line break was not repaired: %q", got)
	}
}
