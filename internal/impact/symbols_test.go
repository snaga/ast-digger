package impact

import (
	"testing"
)

func TestAuditFileSymbols_Golang(t *testing.T) {
	oldGo := []byte(`package main

// OldService represents a service.
type OldService struct{}

// Process handles items.
func (s *OldService) Process() error {
	return nil
}

func privateHelper() int {
	return 42
}
`)

	newGo := []byte(`package main

// OldService represents a service.
type OldService struct{}

// Process handles items with new logic.
func (s *OldService) Process() error {
	x := 10
	if x > 5 {
		return nil
	}
	return nil
}

func privateHelper() int {
	return 42
}

func NewAddedFunc(x int) int {
	return x * 2
}
`)

	// In newGo:
	// lines 8-15: Process() modified
	// lines 21-23: NewAddedFunc added
	info := &FileDiffInfo{
		FilePath:   "sample.go",
		OldContent: oldGo,
		NewContent: newGo,
		AddedLines: []int{8, 9, 10, 11, 21, 22, 23},
	}

	tests := []struct {
		name             string
		req              DocAuditRequirement
		wantMissing      []string
		wantPubCoverage  float64
		wantPrivCoverage float64
		wantImpactCount  int
	}{
		{
			name:             "exported requirement (default)",
			req:              DocAuditExported,
			wantMissing:      []string{"sample.go::NewAddedFunc"},
			wantPubCoverage:  0.5, // 1 documented (Process) out of 2 public (Process, NewAddedFunc)
			wantPrivCoverage: 1.0, // 0 private touched
			wantImpactCount:  2,   // Process (modified), NewAddedFunc (added)
		},
		{
			name:             "all requirement",
			req:              DocAuditAll,
			wantMissing:      []string{"sample.go::NewAddedFunc"},
			wantPubCoverage:  0.5,
			wantPrivCoverage: 1.0,
			wantImpactCount:  2,
		},
		{
			name:             "none requirement",
			req:              DocAuditNone,
			wantMissing:      []string{},
			wantPubCoverage:  0.5,
			wantPrivCoverage: 1.0,
			wantImpactCount:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			impacts, audit, err := AuditFileSymbols(info, tt.req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(impacts) != tt.wantImpactCount {
				t.Fatalf("got %d impacts, want %d", len(impacts), tt.wantImpactCount)
			}

			// Verify specific symbol details
			var foundProcess, foundNewAdded bool
			for _, imp := range impacts {
				if imp.SymbolPath == "OldService.Process" {
					foundProcess = true
					if imp.ChangeType != "modified" {
						t.Errorf("Process change_type = %s, want modified", imp.ChangeType)
					}
					if imp.Visibility != "public" {
						t.Errorf("Process visibility = %s, want public", imp.Visibility)
					}
					if !imp.HasDocstring {
						t.Errorf("Process has_docstring = false, want true")
					}
					if imp.StatementsDelta <= 0 {
						t.Errorf("Process statements_delta = %d, want > 0", imp.StatementsDelta)
					}
				}
				if imp.SymbolPath == "NewAddedFunc" {
					foundNewAdded = true
					if imp.ChangeType != "added" {
						t.Errorf("NewAddedFunc change_type = %s, want added", imp.ChangeType)
					}
					if imp.Visibility != "public" {
						t.Errorf("NewAddedFunc visibility = %s, want public", imp.Visibility)
					}
					if imp.HasDocstring {
						t.Errorf("NewAddedFunc has_docstring = true, want false")
					}
					if imp.StatementsDelta <= 0 {
						t.Errorf("NewAddedFunc statements_delta = %d, want > 0", imp.StatementsDelta)
					}
				}
			}

			if !foundProcess || !foundNewAdded {
				t.Errorf("expected symbols OldService.Process (%v) and NewAddedFunc (%v) to be detected", foundProcess, foundNewAdded)
			}

			if audit.PublicCoverage != tt.wantPubCoverage {
				t.Errorf("PublicCoverage = %v, want %v", audit.PublicCoverage, tt.wantPubCoverage)
			}
			if audit.PrivateCoverage != tt.wantPrivCoverage {
				t.Errorf("PrivateCoverage = %v, want %v", audit.PrivateCoverage, tt.wantPrivCoverage)
			}

			if len(audit.MissingDocstrings) != len(tt.wantMissing) {
				t.Fatalf("MissingDocstrings len = %d, want %d (%v vs %v)", len(audit.MissingDocstrings), len(tt.wantMissing), audit.MissingDocstrings, tt.wantMissing)
			}
			for i, m := range audit.MissingDocstrings {
				if m != tt.wantMissing[i] {
					t.Errorf("MissingDocstrings[%d] = %s, want %s", i, m, tt.wantMissing[i])
				}
			}
		})
	}
}

func TestAuditFileSymbols_Python(t *testing.T) {
	oldPy := []byte(`class Calculator:
    """Calculator docstring."""

    def add(self, a, b):
        """Add two numbers."""
        return a + b

    def _internal_calc(self, x):
        return x * 2
`)

	newPy := []byte(`class Calculator:
    """Calculator docstring."""

    def add(self, a, b):
        """Add two numbers with check."""
        if a < 0:
            return 0
        return a + b

    def _new_private(self):
        return 1
`)

	info := &FileDiffInfo{
		FilePath:     "calculator.py",
		OldContent:   oldPy,
		NewContent:   newPy,
		AddedLines:   []int{6, 7, 8, 10, 11},
		DeletedLines: []int{8, 9},
	}

	impacts, audit, err := AuditFileSymbols(info, DocAuditAll)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain:
	// - Calculator.add: modified, public, has_docstring: true
	// - Calculator._internal_calc: deleted, private, has_docstring: false
	// - Calculator._new_private: added, private, has_docstring: false
	var foundAdd, foundDeleted, foundNewPrivate bool
	for _, imp := range impacts {
		switch imp.SymbolPath {
		case "Calculator.add":
			foundAdd = true
			if imp.ChangeType != "modified" || imp.Visibility != "public" || !imp.HasDocstring {
				t.Errorf("Calculator.add unexpected: %+v", imp)
			}
		case "Calculator._internal_calc":
			foundDeleted = true
			if imp.ChangeType != "deleted" || imp.Visibility != "private" {
				t.Errorf("Calculator._internal_calc unexpected: %+v", imp)
			}
		case "Calculator._new_private":
			foundNewPrivate = true
			if imp.ChangeType != "added" || imp.Visibility != "private" || imp.HasDocstring {
				t.Errorf("Calculator._new_private unexpected: %+v", imp)
			}
		}
	}

	if !foundAdd || !foundDeleted || !foundNewPrivate {
		t.Errorf("missing detected symbols: add=%v, deleted=%v, newPrivate=%v", foundAdd, foundDeleted, foundNewPrivate)
	}

	// Under DocAuditAll:
	// Public: 1 total, 1 documented -> 1.0
	// Private: 1 added (Calculator._new_private), 0 documented -> 0.0
	// Missing: ["calculator.py::Calculator._new_private"]
	if audit.PublicCoverage != 1.0 {
		t.Errorf("PublicCoverage = %v, want 1.0", audit.PublicCoverage)
	}
	if audit.PrivateCoverage != 0.0 {
		t.Errorf("PrivateCoverage = %v, want 0.0", audit.PrivateCoverage)
	}
	if len(audit.MissingDocstrings) != 1 || audit.MissingDocstrings[0] != "calculator.py::Calculator._new_private" {
		t.Errorf("MissingDocstrings = %v, want [calculator.py::Calculator._new_private]", audit.MissingDocstrings)
	}

	// Now verify with DocAuditExported
	_, auditExp, err := AuditFileSymbols(info, DocAuditExported)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(auditExp.MissingDocstrings) != 0 {
		t.Errorf("DocAuditExported missing = %v, want empty", auditExp.MissingDocstrings)
	}
}

func TestAuditFileSymbols_TypeScript(t *testing.T) {
	oldTS := []byte(`/** User API client. */
export class UserAPI {
    /** Fetch user by ID */
    async getUser(id: string): Promise<string> {
        return id;
    }
}
`)

	newTS := []byte(`/** User API client. */
export class UserAPI {
    /** Fetch user by ID */
    async getUser(id: string): Promise<string> {
        if (!id) {
            throw new Error("empty id");
        }
        return id;
    }

    private helperMethod(): void {
        console.log("private");
    }
}

export function newTopLevelFunc(): void {
    console.log("no doc");
}
`)

	info := &FileDiffInfo{
		FilePath:   "user.ts",
		OldContent: oldTS,
		NewContent: newTS,
		AddedLines: []int{5, 6, 7, 11, 12, 13, 16, 17, 18},
	}

	impacts, audit, err := AuditFileSymbols(info, DocAuditExported)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var foundGetUser, foundHelper, foundTopLevel bool
	for _, imp := range impacts {
		switch imp.SymbolPath {
		case "UserAPI.getUser":
			foundGetUser = true
			if imp.Visibility != "public" || imp.ChangeType != "modified" || !imp.HasDocstring {
				t.Errorf("getUser mismatch: %+v", imp)
			}
		case "UserAPI.helperMethod":
			foundHelper = true
			if imp.Visibility != "private" || imp.ChangeType != "added" || imp.HasDocstring {
				t.Errorf("helperMethod mismatch: %+v", imp)
			}
		case "newTopLevelFunc":
			foundTopLevel = true
			if imp.Visibility != "public" || imp.ChangeType != "added" || imp.HasDocstring {
				t.Errorf("newTopLevelFunc mismatch: %+v", imp)
			}
		}
	}

	if !foundGetUser || !foundHelper || !foundTopLevel {
		t.Errorf("symbols missing: getUser=%v, helper=%v, topLevel=%v", foundGetUser, foundHelper, foundTopLevel)
	}

	// DocAuditExported:
	// Public symbols: UserAPI (doc=true), UserAPI.getUser (doc=true), newTopLevelFunc (doc=false) -> 2/3
	// Missing: user.ts::newTopLevelFunc
	wantCoverage := 2.0 / 3.0
	if audit.PublicCoverage != wantCoverage {
		t.Errorf("PublicCoverage = %v, want %v", audit.PublicCoverage, wantCoverage)
	}
	if len(audit.MissingDocstrings) != 1 || audit.MissingDocstrings[0] != "user.ts::newTopLevelFunc" {
		t.Errorf("MissingDocstrings = %v, want [user.ts::newTopLevelFunc]", audit.MissingDocstrings)
	}
}

func TestAuditFileSymbols_Java(t *testing.T) {
	oldJava := []byte(`package com.example;

/** Application main entry. */
public class App {
    public void run() {}
}
`)

	newJava := []byte(`package com.example;

/** Application main entry. */
public class App {
    /** Run application. */
    public void run() {
        System.out.println("running");
    }

    private void internalHelper() {}
}
`)

	info := &FileDiffInfo{
		FilePath:   "App.java",
		OldContent: oldJava,
		NewContent: newJava,
		AddedLines: []int{5, 6, 7, 8, 9, 11},
	}

	impacts, audit, err := AuditFileSymbols(info, DocAuditExported)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var foundRun, foundHelper bool
	for _, imp := range impacts {
		if imp.SymbolPath == "App.run" {
			foundRun = true
			if imp.Visibility != "public" || imp.ChangeType != "modified" || !imp.HasDocstring {
				t.Errorf("App.run mismatch: %+v", imp)
			}
		}
		if imp.SymbolPath == "App.internalHelper" {
			foundHelper = true
			if imp.Visibility != "private" || imp.ChangeType != "added" {
				t.Errorf("App.internalHelper mismatch: %+v", imp)
			}
		}
	}
	if !foundRun || !foundHelper {
		t.Errorf("expected App.run (%v) and App.internalHelper (%v)", foundRun, foundHelper)
	}

	// run has docstring, App class was not modified with changes (or has no added lines in its signature), so public coverage is 1.0
	if audit.PublicCoverage < 0.99 && len(audit.MissingDocstrings) > 0 {
		t.Errorf("audit unexpected: %+v", audit)
	}
}

func TestAuditFileSymbols_Rust(t *testing.T) {
	oldRust := []byte(`pub struct Greeter;

impl Greeter {
    pub fn greet(&self) {}
}
`)

	newRust := []byte(`pub struct Greeter;

impl Greeter {
    /// Greet someone.
    pub fn greet(&self) {
        println!("hello");
    }

    fn secret(&self) {}
}
`)

	info := &FileDiffInfo{
		FilePath:   "greeter.rs",
		OldContent: oldRust,
		NewContent: newRust,
		AddedLines: []int{4, 5, 6, 7, 9},
	}

	impacts, audit, err := AuditFileSymbols(info, DocAuditExported)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var foundGreet, foundSecret bool
	for _, imp := range impacts {
		if imp.SymbolPath == "Greeter.greet" {
			foundGreet = true
			if imp.Visibility != "public" || imp.ChangeType != "modified" || !imp.HasDocstring {
				t.Errorf("greet mismatch: %+v", imp)
			}
		}
		if imp.SymbolPath == "Greeter.secret" {
			foundSecret = true
			if imp.Visibility != "private" || imp.ChangeType != "added" {
				t.Errorf("secret mismatch: %+v", imp)
			}
		}
	}
	if !foundGreet || !foundSecret {
		t.Errorf("expected Greeter.greet (%v) and Greeter.secret (%v)", foundGreet, foundSecret)
	}
	if len(audit.MissingDocstrings) != 0 {
		t.Errorf("expected no missing docstrings for exported, got: %v", audit.MissingDocstrings)
	}
}

func TestAuditFileSymbols_UnsupportedOrEmpty(t *testing.T) {
	// 1. Nil info
	impacts, audit, err := AuditFileSymbols(nil, DocAuditExported)
	if err != nil || impacts != nil || audit != nil {
		t.Errorf("expected nil for nil info, got impacts=%v, audit=%v, err=%v", impacts, audit, err)
	}

	// 2. Unsupported extension (e.g. .txt)
	infoTxt := &FileDiffInfo{
		FilePath:   "notes.txt",
		NewContent: []byte("hello world"),
		AddedLines: []int{1},
	}
	impacts, audit, err = AuditFileSymbols(infoTxt, DocAuditExported)
	if err != nil || impacts != nil || audit != nil {
		t.Errorf("expected nil for unsupported file, got impacts=%v, audit=%v, err=%v", impacts, audit, err)
	}
}
