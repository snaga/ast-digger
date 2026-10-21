package parser

import "testing"

// TestSymbolDataInitialization verifies that SymbolData can be constructed
// both with all optional pointer fields populated and with them left nil.
func TestSymbolDataInitialization(t *testing.T) {
	name := "parent-symbol"
	filePath := "/tmp/example.py"
	signature := "(self, arg: int) -> None"
	docstring := "Does a thing."
	code := "def register_user(self, arg: int) -> None:\n    ..."

	tests := []struct {
		name string
		data SymbolData
		want SymbolData
	}{
		{
			name: "fully populated",
			data: SymbolData{
				Name:      "register_user",
				Kind:      "method",
				Parent:    &name,
				FilePath:  &filePath,
				StartLine: 10,
				EndLine:   20,
				Signature: &signature,
				Docstring: &docstring,
				Code:      &code,
			},
			want: SymbolData{
				Name:      "register_user",
				Kind:      "method",
				Parent:    &name,
				FilePath:  &filePath,
				StartLine: 10,
				EndLine:   20,
				Signature: &signature,
				Docstring: &docstring,
				Code:      &code,
			},
		},
		{
			name: "optional fields nil",
			data: SymbolData{
				Name:      "UserManager",
				Kind:      "class",
				StartLine: 1,
				EndLine:   5,
			},
			want: SymbolData{
				Name:      "UserManager",
				Kind:      "class",
				StartLine: 1,
				EndLine:   5,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.data.Name != tt.want.Name {
				t.Errorf("Name = %q, want %q", tt.data.Name, tt.want.Name)
			}
			if tt.data.Kind != tt.want.Kind {
				t.Errorf("Kind = %q, want %q", tt.data.Kind, tt.want.Kind)
			}
			if tt.data.StartLine != tt.want.StartLine {
				t.Errorf("StartLine = %d, want %d", tt.data.StartLine, tt.want.StartLine)
			}
			if tt.data.EndLine != tt.want.EndLine {
				t.Errorf("EndLine = %d, want %d", tt.data.EndLine, tt.want.EndLine)
			}
			if (tt.data.Parent == nil) != (tt.want.Parent == nil) {
				t.Errorf("Parent nil-ness mismatch: got %v, want %v", tt.data.Parent, tt.want.Parent)
			}
			if tt.data.Parent != nil && *tt.data.Parent != *tt.want.Parent {
				t.Errorf("*Parent = %q, want %q", *tt.data.Parent, *tt.want.Parent)
			}
			if (tt.data.FilePath == nil) != (tt.want.FilePath == nil) {
				t.Errorf("FilePath nil-ness mismatch: got %v, want %v", tt.data.FilePath, tt.want.FilePath)
			}
			if tt.data.FilePath != nil && *tt.data.FilePath != *tt.want.FilePath {
				t.Errorf("*FilePath = %q, want %q", *tt.data.FilePath, *tt.want.FilePath)
			}
			if (tt.data.Signature == nil) != (tt.want.Signature == nil) {
				t.Errorf("Signature nil-ness mismatch: got %v, want %v", tt.data.Signature, tt.want.Signature)
			}
			if tt.data.Signature != nil && *tt.data.Signature != *tt.want.Signature {
				t.Errorf("*Signature = %q, want %q", *tt.data.Signature, *tt.want.Signature)
			}
			if (tt.data.Docstring == nil) != (tt.want.Docstring == nil) {
				t.Errorf("Docstring nil-ness mismatch: got %v, want %v", tt.data.Docstring, tt.want.Docstring)
			}
			if tt.data.Docstring != nil && *tt.data.Docstring != *tt.want.Docstring {
				t.Errorf("*Docstring = %q, want %q", *tt.data.Docstring, *tt.want.Docstring)
			}
			if (tt.data.Code == nil) != (tt.want.Code == nil) {
				t.Errorf("Code nil-ness mismatch: got %v, want %v", tt.data.Code, tt.want.Code)
			}
			if tt.data.Code != nil && *tt.data.Code != *tt.want.Code {
				t.Errorf("*Code = %q, want %q", *tt.data.Code, *tt.want.Code)
			}
		})
	}
}

// TestReferenceDataInitialization verifies that ReferenceData can be
// constructed with its required fields.
func TestReferenceDataInitialization(t *testing.T) {
	ref := ReferenceData{
		FilePath:    "/tmp/example.py",
		LineNumber:  42,
		LineContent: "register_user(user)",
	}

	if ref.FilePath != "/tmp/example.py" {
		t.Errorf("FilePath = %q, want %q", ref.FilePath, "/tmp/example.py")
	}
	if ref.LineNumber != 42 {
		t.Errorf("LineNumber = %d, want %d", ref.LineNumber, 42)
	}
	if ref.LineContent != "register_user(user)" {
		t.Errorf("LineContent = %q, want %q", ref.LineContent, "register_user(user)")
	}
}

// stubParser is a minimal Parser implementation used only to verify, at
// compile time, that the Parser interface's method set matches the design.
type stubParser struct{}

func (stubParser) ParseFile(filePath string) ([]SymbolData, error) {
	return nil, nil
}

func (stubParser) ParseBytes(source []byte, filePath string) ([]SymbolData, error) {
	return nil, nil
}

func (stubParser) ExtractSymbol(filePath, symbolPath string) (SymbolData, error) {
	return SymbolData{}, nil
}

func (stubParser) ResolveImports(filePath, symbolName string) (string, int, error) {
	return "", 0, nil
}

func (stubParser) FindReferencesInFile(filePath, symbolName string) ([]ReferenceData, error) {
	return nil, nil
}

func (stubParser) GenerateSkeleton(filePath string) (string, error) {
	return "", nil
}

// TestParserInterfaceSatisfiedByStub verifies that a concrete type
// implementing the four Parser methods satisfies the Parser interface.
func TestParserInterfaceSatisfiedByStub(t *testing.T) {
	var p Parser = stubParser{}

	if _, err := p.ParseFile("file.py"); err != nil {
		t.Errorf("ParseFile returned unexpected error: %v", err)
	}
	if _, err := p.ExtractSymbol("file.py", "Foo.bar"); err != nil {
		t.Errorf("ExtractSymbol returned unexpected error: %v", err)
	}
	if _, _, err := p.ResolveImports("file.py", "bar"); err != nil {
		t.Errorf("ResolveImports returned unexpected error: %v", err)
	}
	if _, err := p.FindReferencesInFile("file.py", "bar"); err != nil {
		t.Errorf("FindReferencesInFile returned unexpected error: %v", err)
	}
	if _, err := p.GenerateSkeleton("file.py"); err != nil {
		t.Errorf("GenerateSkeleton returned unexpected error: %v", err)
	}
}
