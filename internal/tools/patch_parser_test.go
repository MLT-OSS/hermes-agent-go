package tools

import (
	"strings"
	"testing"
)

func TestParseUnifiedDiff(t *testing.T) {
	diff := `--- a/hello.go
+++ b/hello.go
@@ -1,3 +1,4 @@
 package main
 
+import "fmt"
 func main() {
@@ -5,3 +6,3 @@
-	println("hello")
+	fmt.Println("hello")
 }
`
	patches, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(patches) != 1 {
		t.Fatalf("expected 1 patch, got %d", len(patches))
	}
	p := patches[0]
	if p.OldPath != "hello.go" {
		t.Errorf("OldPath = %q, want \"hello.go\"", p.OldPath)
	}
	if p.NewPath != "hello.go" {
		t.Errorf("NewPath = %q, want \"hello.go\"", p.NewPath)
	}
	if len(p.Hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %d", len(p.Hunks))
	}
	if p.Hunks[0].OldStart != 1 {
		t.Errorf("Hunk[0].OldStart = %d, want 1", p.Hunks[0].OldStart)
	}
}

func TestApplyHunks(t *testing.T) {
	original := "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"

	hunks := []PatchHunk{
		{
			OldStart: 1,
			OldCount: 3,
			NewStart: 1,
			NewCount: 4,
			Lines: []DiffLine{
				{Op: ' ', Text: "package main"},
				{Op: ' ', Text: ""},
				{Op: '+', Text: "import \"fmt\""},
				{Op: ' ', Text: "func main() {"},
			},
		},
	}

	result, conflicts := ApplyHunks(original, hunks)
	if len(conflicts) != 0 {
		t.Errorf("unexpected conflicts: %v", conflicts)
	}
	if !strings.Contains(result, "import \"fmt\"") {
		t.Errorf("result should contain import, got:\n%s", result)
	}
}

func TestApplyHunks_Conflict(t *testing.T) {
	original := "line1\nline2\nline3\n"

	hunks := []PatchHunk{
		{
			OldStart: 1,
			OldCount: 1,
			NewStart: 1,
			NewCount: 1,
			Lines: []DiffLine{
				{Op: '-', Text: "wrong_line"},
				{Op: '+', Text: "new_line"},
			},
		},
	}

	_, conflicts := ApplyHunks(original, hunks)
	if len(conflicts) == 0 {
		t.Error("expected conflict for mismatched context")
	}
}

func TestParseUnifiedDiff_Empty(t *testing.T) {
	patches, err := ParseUnifiedDiff("")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(patches) != 0 {
		t.Errorf("expected 0 patches, got %d", len(patches))
	}
}

func TestStripPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"a/src/main.go", "src/main.go"},
		{"b/src/main.go", "src/main.go"},
		{"src/main.go", "src/main.go"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stripPrefix(tt.input)
			if got != tt.want {
				t.Errorf("stripPrefix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
