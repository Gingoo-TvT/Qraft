package generationapi

import "testing"

func TestJobsV1RejectsUnwiredOutputAndLanguageCapabilities(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Request)
	}{
		{
			name: "hydro output",
			mutate: func(request *Request) {
				request.Output.Formats = []string{"hydro"}
			},
		},
		{
			name: "multiple output formats",
			mutate: func(request *Request) {
				request.Output.Formats = []string{"algoforge", "hydro"}
			},
		},
		{
			name: "subtask problem",
			mutate: func(request *Request) {
				request.ProblemType = ProblemTypeSubtask
			},
		},
		{
			name: "unsupported language",
			mutate: func(request *Request) {
				request.Languages = []string{"brainfuck"}
			},
		},
		{
			name: "editorial exclusion",
			mutate: func(request *Request) {
				request.Output.IncludeEditorial = false
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := loadFixtureRequest(t)
			test.mutate(&request)
			err := request.ValidateV1()
			if err == nil {
				t.Fatal("ValidateV1 accepted an unsupported product capability")
			}
			if got := ErrorCodeOf(err); got != ErrorUnsupportedConstraint {
				t.Fatalf("error code = %q, want %q (err=%v)", got, ErrorUnsupportedConstraint, err)
			}
		})
	}
}

func TestJobsV1AcceptsOnlyCanonicalSandboxLanguages(t *testing.T) {
	for _, language := range []string{"c", "cpp", "python3", "java", "go"} {
		t.Run(language, func(t *testing.T) {
			request := loadFixtureRequest(t)
			request.Languages = []string{language}
			if err := request.ValidateV1(); err != nil {
				t.Fatalf("ValidateV1(%q) error = %v", language, err)
			}
		})
	}

	request := loadFixtureRequest(t)
	request.Languages = []string{" CPP "}
	request.Output.Formats = []string{" ALGOFORGE "}
	if err := request.ValidateV1(); err != nil {
		t.Fatalf("case-normalized stable controls were rejected: %v", err)
	}
}
