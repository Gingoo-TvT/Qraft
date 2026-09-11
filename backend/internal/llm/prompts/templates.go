package prompts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
)

// ---------------------------------------------------------------------------
// PromptTemplate
// ---------------------------------------------------------------------------

// PromptTemplate represents a reusable prompt template composed of a system
// prompt and a user prompt template that is rendered with Go text/template.
const PromptVersionV1 = "v1"

type PromptTemplate struct {
	// Name uniquely identifies this template within the registry.
	Name string

	// Version identifies the server-owned prompt contract revision.
	Version string

	// SystemPrompt is the static system-level instruction sent to Claude.
	SystemPrompt string

	// UserPromptTemplate is a Go text/template string that will be rendered
	// with a context-specific data struct to produce the user message.
	UserPromptTemplate string

	// compiled is the lazily-compiled template.
	compiled *template.Template
}

// Render executes the user prompt template with the given data and returns
// an llm.Request ready to be sent to the Anthropic API.
type PromptDescriptor struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

type promptSourceIdentity struct {
	ID           string `json:"id"`
	Version      string `json:"version"`
	SystemPrompt string `json:"system_prompt"`
	UserTemplate string `json:"user_template"`
}

func (pt *PromptTemplate) effectiveVersion() string {
	if strings.TrimSpace(pt.Version) == "" {
		return PromptVersionV1
	}
	return strings.TrimSpace(pt.Version)
}

// Descriptor returns a stable digest of the prompt source, independent of
// rendered request data.
func (pt *PromptTemplate) Descriptor() PromptDescriptor {
	identity := promptSourceIdentity{ID: pt.Name, Version: pt.effectiveVersion(), SystemPrompt: pt.SystemPrompt, UserTemplate: pt.UserPromptTemplate}
	encoded, _ := json.Marshal(identity)
	digest := sha256.Sum256(encoded)
	return PromptDescriptor{ID: pt.Name, Version: pt.effectiveVersion(), Digest: hex.EncodeToString(digest[:])}
}

func (pt *PromptTemplate) Render(data interface{}) (*llm.Request, error) {
	if pt.compiled == nil {
		tmpl, err := template.New(pt.Name).
			Funcs(templateFuncs).
			Parse(pt.UserPromptTemplate)
		if err != nil {
			return nil, fmt.Errorf("compiling template %q: %w", pt.Name, err)
		}
		pt.compiled = tmpl
	}

	var buf bytes.Buffer
	if err := pt.compiled.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing template %q: %w", pt.Name, err)
	}

	return &llm.Request{
		PromptID:      pt.Name,
		PromptVersion: pt.effectiveVersion(),
		System:        pt.SystemPrompt,
		Messages: []llm.Message{
			{Role: "user", Content: buf.String()},
		},
	}, nil
}

// RenderWithMessages is like Render but allows appending additional assistant
// or user messages (e.g. for few-shot examples) before the final user prompt.
func (pt *PromptTemplate) RenderWithMessages(data interface{}, prefixMessages []llm.Message) (*llm.Request, error) {
	req, err := pt.Render(data)
	if err != nil {
		return nil, err
	}

	// The final user message produced by Render becomes the last message.
	finalMsg := req.Messages[0]
	req.Messages = append(prefixMessages, finalMsg)
	return req, nil
}

// ---------------------------------------------------------------------------
// Template functions
// ---------------------------------------------------------------------------

// templateFuncs provides helper functions available inside templates.
var templateFuncs = template.FuncMap{
	"join":       strings.Join,
	"lower":      strings.ToLower,
	"upper":      strings.ToUpper,
	"trimSpace":  strings.TrimSpace,
	"contains":   strings.Contains,
	"jsonEscape": jsonEscape,
}

// jsonEscape escapes a string for safe inclusion in a JSON value.
func jsonEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}

// ---------------------------------------------------------------------------
// Level context helper
// ---------------------------------------------------------------------------

// LevelContext holds level-specific metadata that is injected into prompts
// to calibrate the LLM's output for either syntax or algorithm problems.
type LevelContext struct {
	// Level is the problem level ("syntax" or "algorithm").
	Level domain.ProblemLevel

	// DifficultyMin and DifficultyMax define the valid difficulty range.
	DifficultyMin int
	DifficultyMax int

	// Description is a human-readable explanation of what this level means.
	Description string

	// AvailableTags lists the tag names valid for this level.
	AvailableTags []string

	// StyleGuidance provides level-specific writing instructions.
	StyleGuidance string
}

// NewLevelContext creates a LevelContext for the given level, populating it
// with available tags and calibration data. Tags are filtered by both level
// and difficulty to ensure only appropriate tags are presented to the LLM.
func NewLevelContext(level domain.ProblemLevel, tags []domain.TagCategory, difficulty ...int) LevelContext {
	minD, maxD := domain.DifficultyRange(level)

	// If a difficulty is provided, filter tags by difficulty range.
	targetDifficulty := 0
	if len(difficulty) > 0 {
		targetDifficulty = difficulty[0]
	}

	var available []string
	for _, t := range tags {
		if t.Level == level {
			// If difficulty filtering is requested, only include tags whose
			// range covers the target difficulty.
			if targetDifficulty > 0 && (t.MinDifficulty > targetDifficulty || t.MaxDifficulty < targetDifficulty) {
				continue
			}
			available = append(available, t.TagName)
		}
	}

	lc := LevelContext{
		Level:         level,
		DifficultyMin: minD,
		DifficultyMax: maxD,
		AvailableTags: available,
	}

	switch level {
	case domain.LevelSyntax:
		lc.Description = "Syntax-level problems focus on basic programming constructs: " +
			"loops, conditionals, string manipulation, simple array operations, and " +
			"basic I/O. They do not require knowledge of algorithms or data structures " +
			"beyond arrays."
		lc.StyleGuidance = "Use clear, beginner-friendly language. Constraints should be " +
			"small (n <= 1000 typically). Avoid advanced algorithmic concepts. The problem " +
			"should be solvable with straightforward code using only basic language features."
	case domain.LevelAlgorithm:
		lc.Description = "Algorithm-level problems require knowledge of algorithms and " +
			"data structures such as dynamic programming, graph theory, segment trees, " +
			"number theory, computational geometry, etc."
		lc.StyleGuidance = "Use precise mathematical language where appropriate. " +
			"Constraints should be carefully calibrated to the intended time complexity. " +
			"The problem should have a clear algorithmic insight or technique as its core."
	case domain.LevelGPLTL1:
		lc.Description = "团体程序设计天梯赛 L1：基础编程题，考查循环、条件、数组、字符串、简单模拟等。真实赛制下 L1 共 8 题、分值分别为 5/5/10/10/15/15/20/20（本题分值由 gplt_score 指定）。"
		lc.StyleGuidance = GPLTStyleHint(domain.GPLTTierL1)
	case domain.LevelGPLTL2:
		lc.Description = "团体程序设计天梯赛 L2：每题 25 分。L2 的关键不在代码量，而在题目是否明确指向某个数据结构或算法（栈/队列/链表/树/并查集/基础图论/基础 DP/二分/贪心）。实现难度可能低于 20 分的 L1 模拟题。"
		lc.StyleGuidance = GPLTStyleHint(domain.GPLTTierL2)
	case domain.LevelGPLTL3:
		lc.Description = "团体程序设计天梯赛 L3：综合难题，考查较深入的算法思维（复杂 DP、图论进阶、数学、数据结构综合应用等）。题目总分 30 分。"
		lc.StyleGuidance = GPLTStyleHint(domain.GPLTTierL3)
	}

	return lc
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

// Registry holds all registered prompt templates, keyed by name.
type Registry struct {
	templates map[string]*PromptTemplate
}

// NewRegistry creates a new Registry pre-populated with all standard
// AlgoForge prompt templates.
func NewRegistry() *Registry {
	r := &Registry{
		templates: make(map[string]*PromptTemplate),
	}

	// Register all standard templates.
	r.Register(StatementTemplate())
	r.Register(SolutionTemplate())
	r.Register(TestDataTemplate())
	r.Register(ReviewTemplate())
	r.Register(EditorialTemplate())

	return r
}

// Register adds a template to the registry.  It panics if a template with
// the same name already exists.
func (r *Registry) Register(t *PromptTemplate) {
	if _, exists := r.templates[t.Name]; exists {
		panic(fmt.Sprintf("duplicate prompt template name: %q", t.Name))
	}
	r.templates[t.Name] = t
}

// Get retrieves a template by name.  It returns an error if the template is
// not found.
func (r *Registry) Get(name string) (*PromptTemplate, error) {
	name = strings.TrimSpace(name)
	aliases := map[string]string{
		"statement": TemplateNameStatement,
		"solution":  TemplateNameSolution,
		"testdata":  TemplateNameTestData,
		"review":    TemplateNameReview,
		"editorial": TemplateNameEditorial,
	}
	if canonical, ok := aliases[name]; ok {
		name = canonical
	}
	t, ok := r.templates[name]
	if !ok {
		return nil, fmt.Errorf("prompt template %q not found", name)
	}
	return t, nil
}

// Names returns the names of all registered templates.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.templates))
	for name := range r.templates {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Descriptor returns the stable source identity for one template.
func (r *Registry) Descriptor(name string) (PromptDescriptor, error) {
	template, err := r.Get(name)
	if err != nil {
		return PromptDescriptor{}, err
	}
	return template.Descriptor(), nil
}

// Descriptors returns all registered prompt identities in stable order.
func (r *Registry) Descriptors() []PromptDescriptor {
	names := r.Names()
	result := make([]PromptDescriptor, 0, len(names))
	for _, name := range names {
		result = append(result, r.templates[name].Descriptor())
	}
	return result
}
