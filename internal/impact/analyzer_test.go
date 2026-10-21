package impact_test

import (
	"testing"

	"github.com/snaga/ast-digger/internal/impact"
)

func TestCalculateImpactLevel(t *testing.T) {
	tests := []struct {
		name            string
		statementChange int
		complexityDelta int
		want            impact.ImpactLevel
	}{
		{
			name:            "minor minimal",
			statementChange: 0,
			complexityDelta: 0,
			want:            impact.ImpactMinor,
		},
		{
			name:            "minor upper boundary",
			statementChange: 19,
			complexityDelta: 2,
			want:            impact.ImpactMinor,
		},
		{
			name:            "moderate due to complexity > 2",
			statementChange: 10,
			complexityDelta: 3,
			want:            impact.ImpactModerate,
		},
		{
			name:            "moderate lower boundary",
			statementChange: 20,
			complexityDelta: 0,
			want:            impact.ImpactModerate,
		},
		{
			name:            "moderate upper boundary",
			statementChange: 60,
			complexityDelta: 1,
			want:            impact.ImpactModerate,
		},
		{
			name:            "significant lower boundary",
			statementChange: 61,
			complexityDelta: 0,
			want:            impact.ImpactSignificant,
		},
		{
			name:            "significant upper boundary",
			statementChange: 150,
			complexityDelta: 5,
			want:            impact.ImpactSignificant,
		},
		{
			name:            "extreme lower boundary",
			statementChange: 151,
			complexityDelta: 0,
			want:            impact.ImpactExtreme,
		},
		{
			name:            "extreme large changes",
			statementChange: 500,
			complexityDelta: 50,
			want:            impact.ImpactExtreme,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := impact.CalculateImpactLevel(tt.statementChange, tt.complexityDelta)
			if got != tt.want {
				t.Errorf("CalculateImpactLevel(%d, %d) = %v, want %v",
					tt.statementChange, tt.complexityDelta, got, tt.want)
			}
		})
	}
}

func TestAnalyzeSource_CommentsAndWhitespacesOnly(t *testing.T) {
	tests := []struct {
		name   string
		ext    string
		oldSrc string
		newSrc string
	}{
		{
			name: "Go: comments and blank lines added",
			ext:  ".go",
			oldSrc: `package main

func add(a, b int) int {
	return a + b
}
`,
			newSrc: `package main

// Package main comment

// add performs integer addition.
func add(a, b int) int {
	// Inside function comment

	return a + b
}
`,
		},
		{
			name: "Python: comments and blank lines added",
			ext:  ".py",
			oldSrc: `def calculate(x, y):
    result = x * y
    return result
`,
			newSrc: `# Top level comment

def calculate(x, y):
    # Multiply x and y

    result = x * y
    # Return result
    return result
`,
		},
		{
			name: "TypeScript: comments and blank lines added",
			ext:  ".ts",
			oldSrc: `export function greet(name: string): string {
    const greeting = "Hello, " + name;
    return greeting;
}
`,
			newSrc: `/**
 * JSDoc comment
 */
export function greet(name: string): string {
    // build greeting

    const greeting = "Hello, " + name;
    /* block comment */
    return greeting;
}
`,
		},
		{
			name: "Rust: comments and blank lines added",
			ext:  ".rs",
			oldSrc: `pub fn add(a: i32, b: i32) -> i32 {
    let sum = a + b;
    sum
}
`,
			newSrc: `// Top doc
pub fn add(a: i32, b: i32) -> i32 {
    // comment
    let sum = a + b;

    // returning
    sum
}
`,
		},
		{
			name: "Java: comments and blank lines added",
			ext:  ".java",
			oldSrc: `public class MathUtil {
    public static int add(int a, int b) {
        return a + b;
    }
}
`,
			newSrc: `// Header comment
public class MathUtil {
    /**
     * JavaDoc
     */
    public static int add(int a, int b) {
        // body comment

        return a + b;
    }
}
`,
		},
		{
			name: "SQL: comments and blank lines added",
			ext:  ".sql",
			oldSrc: `SELECT id, name FROM users WHERE active = 1;
`,
			newSrc: `-- Query all active users
SELECT id, name
FROM users

-- Filter clause
WHERE active = 1;
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := impact.AnalyzeSource(tt.ext, []byte(tt.oldSrc), []byte(tt.newSrc))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if res.Statements.Added != 0 {
				t.Errorf("expected Statements.Added == 0, got %d", res.Statements.Added)
			}
			if res.Statements.Deleted != 0 {
				t.Errorf("expected Statements.Deleted == 0, got %d", res.Statements.Deleted)
			}
			if res.ComplexityDelta != 0 {
				t.Errorf("expected ComplexityDelta == 0, got %d", res.ComplexityDelta)
			}
			if res.ImpactLevel != impact.ImpactMinor {
				t.Errorf("expected ImpactLevel == %s, got %s", impact.ImpactMinor, res.ImpactLevel)
			}
		})
	}
}

func TestAnalyzeSource_LogicModifications(t *testing.T) {
	tests := []struct {
		name                string
		ext                 string
		oldSrc              string
		newSrc              string
		wantMinAdded        int
		wantComplexityDelta int
		wantImpact          impact.ImpactLevel
	}{
		{
			name: "Go: adding if statement and branch",
			ext:  ".go",
			oldSrc: `package main

func check(x int) int {
	return x
}
`,
			newSrc: `package main

func check(x int) int {
	if x < 0 {
		return 0
	}
	return x
}
`,
			wantMinAdded:        2, // if_statement, return_statement
			wantComplexityDelta: 1, // if_statement
			wantImpact:          impact.ImpactMinor,
		},
		{
			name: "Python: adding for loop and print",
			ext:  ".py",
			oldSrc: `def process(items):
    return len(items)
`,
			newSrc: `def process(items):
    for item in items:
        print(item)
    return len(items)
`,
			wantMinAdded:        2, // for_statement, expression_statement
			wantComplexityDelta: 1, // for_statement
			wantImpact:          impact.ImpactMinor,
		},
		{
			name: "TypeScript: adding try-catch and switch statement",
			ext:  ".ts",
			oldSrc: `export function handle(action: string) {
    console.log(action);
}
`,
			newSrc: `export function handle(action: string) {
    try {
        switch (action) {
            case "start":
                console.log("starting");
                break;
            default:
                console.log("unknown");
        }
    } catch (err) {
        console.error(err);
    }
}
`,
			wantMinAdded:        5,                     // try_statement, catch_clause, switch_statement, etc.
			wantComplexityDelta: 3,                     // try/catch (catch_clause=1), switch_statement=1, case_clause=1 -> >= 3
			wantImpact:          impact.ImpactModerate, // complexity > 2 triggers MODERATE even if stmts < 20
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := impact.AnalyzeSource(tt.ext, []byte(tt.oldSrc), []byte(tt.newSrc))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if res.Statements.Added < tt.wantMinAdded {
				t.Errorf("expected Statements.Added >= %d, got %d", tt.wantMinAdded, res.Statements.Added)
			}
			if res.ComplexityDelta != tt.wantComplexityDelta {
				t.Errorf("expected ComplexityDelta == %d, got %d", tt.wantComplexityDelta, res.ComplexityDelta)
			}
			if res.ImpactLevel != tt.wantImpact {
				t.Errorf("expected ImpactLevel == %s, got %s", tt.wantImpact, res.ImpactLevel)
			}
		})
	}
}

func TestAnalyzeSource_DeletionAndEdgeCases(t *testing.T) {
	t.Run("Go: deleting branch logic", func(t *testing.T) {
		oldSrc := `package main

func check(x int) int {
	if x < 0 {
		return 0
	}
	return x
}
`
		newSrc := `package main

func check(x int) int {
	return x
}
`
		res, err := impact.AnalyzeSource("go", []byte(oldSrc), []byte(newSrc))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if res.Statements.Deleted < 2 {
			t.Errorf("expected Statements.Deleted >= 2, got %d", res.Statements.Deleted)
		}
		if res.Statements.Added != 0 {
			t.Errorf("expected Statements.Added == 0, got %d", res.Statements.Added)
		}
		if res.ComplexityDelta != -1 {
			t.Errorf("expected ComplexityDelta == -1, got %d", res.ComplexityDelta)
		}
	})

	t.Run("New file created (oldSrc empty)", func(t *testing.T) {
		newSrc := `package main

func hello() {
	println("hello")
}
`
		res, err := impact.AnalyzeSource("go", nil, []byte(newSrc))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if res.Statements.Added <= 0 {
			t.Errorf("expected Statements.Added > 0, got %d", res.Statements.Added)
		}
		if res.Statements.Deleted != 0 {
			t.Errorf("expected Statements.Deleted == 0, got %d", res.Statements.Deleted)
		}
	})

	t.Run("File deleted (newSrc empty)", func(t *testing.T) {
		oldSrc := `package main

func hello() {
	println("hello")
}
`
		res, err := impact.AnalyzeSource("go", []byte(oldSrc), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if res.Statements.Deleted <= 0 {
			t.Errorf("expected Statements.Deleted > 0, got %d", res.Statements.Deleted)
		}
		if res.Statements.Added != 0 {
			t.Errorf("expected Statements.Added == 0, got %d", res.Statements.Added)
		}
	})

	t.Run("Unsupported file extension", func(t *testing.T) {
		_, err := impact.AnalyzeSource(".unsupported_ext", []byte("a"), []byte("b"))
		if err == nil {
			t.Fatal("expected error for unsupported extension, got nil")
		}
	})
}
