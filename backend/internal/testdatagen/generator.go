// Package testdatagen provides reusable generator recipes and sandbox tools.
// Generated code only executes in the independent sandbox.
package testdatagen

import (
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

const Version = "algoforge.testdata.v1"
const MaxCodeBytes = 64 << 10
const MaxCaseBytes int64 = 8 << 20
const MaxTotalBytes int64 = 32 << 20
const DefaultCaseBytes int64 = 1 << 20
const MaxCases = 32

//go:embed generator.hpp
var header string

type Recipe struct {
	Version string `json:"version"`
	Code    string `json:"code"`
}
type Generator struct {
	Recipe        Recipe `json:"recipe"`
	Source        string `json:"source"`
	SHA256        string `json:"sha256"`
	LibrarySHA256 string `json:"library_sha256"`
}

func SHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// Build assembles source; compilation belongs to the sandbox.
func Build(recipe Recipe) (*Generator, error) {
	if recipe.Version != Version {
		return nil, fmt.Errorf("unsupported generator recipe version %q", recipe.Version)
	}
	if strings.TrimSpace(recipe.Code) == "" || len(recipe.Code) > MaxCodeBytes {
		return nil, fmt.Errorf("generator recipe code must contain 1..%d bytes", MaxCodeBytes)
	}
	source := header + "\n#line 1 \"recipe.cpp\"\n" + recipe.Code + "\n" + harness
	return &Generator{Recipe: recipe, Source: source, SHA256: SHA256(source), LibrarySHA256: SHA256(header)}, nil
}

// Seed preserves the existing workflow's source/index/group identity.
func Seed(source string, index, group int) int64 {
	hash := sha256.New()
	_, _ = hash.Write([]byte("algoforge-generator-seed-v1\x00"))
	_, _ = hash.Write([]byte(source))
	var fields [16]byte
	binary.BigEndian.PutUint64(fields[:8], uint64(int64(index)))
	binary.BigEndian.PutUint64(fields[8:], uint64(int64(group)))
	_, _ = hash.Write(fields[:])
	seed := int64(binary.BigEndian.Uint64(hash.Sum(nil)[:8]) & uint64(^uint64(0)>>1))
	if seed == 0 {
		return 1
	}
	return seed
}

// BatchSize bounds worst-case JSON escaping below the 64 MiB transport limit.
func BatchSize(outputLimit int64) (int, error) {
	if outputLimit <= 0 || outputLimit > MaxCaseBytes {
		return 0, fmt.Errorf("output_limit_bytes must be in [1,%d]", MaxCaseBytes)
	}
	n := int(MaxCaseBytes / outputLimit)
	if n > MaxCases {
		n = MaxCases
	}
	return n, nil
}

const harness = `
#line 1 "algoforge_harness.cpp"
int main() {
    std::ios::sync_with_stdio(false);
    std::cin.tie(nullptr);
    try {
        long long index, group, seed;
        if (!(std::cin >> index >> group >> seed)) throw std::invalid_argument("expected test_index group_id seed");
        std::string extra;
        if (std::cin >> extra) throw std::invalid_argument("unexpected generator input");
        if (index < 0 || seed < 0) throw std::invalid_argument("index and seed must be nonnegative");
        af::Random rng(static_cast<std::uint64_t>(seed));
        generate(index, group, rng, std::cout);
        std::cout.flush();
        if (!std::cout) throw std::runtime_error("writing generated input failed");
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 2;
    }
}
`

// Prompt is shared by the live activity and registered prompt templates.
const Prompt = `
Reusable generation framework (preferred):
- Return generator_recipe: {"version":"algoforge.testdata.v1","code":"..."}.
- Its code defines exactly:
  void generate(long long test_index, long long group_id, af::Random& rng, std::ostream& out)
- The server supplies C++20, the library and main(). Do not emit main(), testlib.h,
  registerGen(), local-file dependencies or an #include for the framework.
- Library API (integer intervals inclusive; vertices 1-based):
  rng.integer(lo,hi); rng.array(n,lo,hi); rng.distinct(n,lo,hi);
  rng.permutation(n); rng.shuffle(vector); rng.text(n,alphabet);
  rng.palindrome(n,alphabet); rng.tree(n,"random"|"chain"|"star"|"binary");
  rng.degree_tree(n,max_degree); rng.graph(n,m,connected); rng.dag(n,m);
  rng.relabel(n,edges); af::line(out,vector); af::print_edges(out,edges).
- binary trees have root 1 with at most two children per node; relabel changes
  that root identity. graph is simple undirected; dag is simple directed acyclic.
  n, m, and vector/string lengths must not exceed 2,000,000.
- Tree/graph randomness does not promise uniform sampling over all structures.
- You remain responsible for input format, constraints linking variables,
  valid operation sequences, edge weights, and adversarial constructions.
  Write short custom C++ when needed; do not force a problem into an unsuitable helper.
- Plan cases from semantic_spec, test_intents and plausible wrong solutions.
  Uniform random data alone is insufficient. Coverage descriptions are intentions,
  never evidence that a property was checked.
- Keep case-index branches aligned with test_cases, including inline samples.
- Set output_limit_bytes per case when needed (default 1048576, max 8388608).
  Final input bytes must obey the existing total size and case-count contract.
- Legacy generator_code is accepted and must be a self-contained C++20 program
  reading test_index group_id seed from stdin.
  Use either generator_recipe or generator_code; never both.
Example recipe code:
void generate(long long i, long long g, af::Random& rng, std::ostream& out) {
    int n = i == 0 ? 1 : (i == 1 ? 100000 : 100);
    auto a = rng.array(n, -1000000000LL, 1000000000LL);
    out << n << '\n';
    af::line(out, a);
}
`
