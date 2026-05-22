package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmitMatrixNDJSON(t *testing.T) {
	rows := []MatrixRow{
		{Target: "mac", Status: "pass", ExitCode: 0, DurationMs: 12},
		{Target: "linux", Status: "fail", ExitCode: 7, DurationMs: 200, Error: "boom", Tail: "panic"},
	}
	var buf bytes.Buffer
	if err := emitMatrix(&buf, rows); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 rows, got %d: %q", len(lines), buf.String())
	}
	var first MatrixRow
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("first row not JSON: %v", err)
	}
	if first.Target != "mac" || first.Status != "pass" {
		t.Errorf("first row corrupted: %+v", first)
	}
	var second MatrixRow
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("second row not JSON: %v", err)
	}
	if second.Status != "fail" || second.Tail != "panic" || second.Error != "boom" {
		t.Errorf("second row corrupted: %+v", second)
	}
}

func TestTailStderrReturnsLastNLines(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "targets", "linux")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	// 50 lines — should be trimmed to matrixTailLines (40).
	for i := 1; i <= 50; i++ {
		sb.WriteString("line ")
		sb.WriteString(itoaSmall(i))
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(targetDir, "stderr.log"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	tail := tailStderr(dir, "linux")
	lines := strings.Split(tail, "\n")
	if len(lines) != matrixTailLines {
		t.Fatalf("expected %d tail lines, got %d", matrixTailLines, len(lines))
	}
	if lines[0] != "line 11" {
		t.Errorf("expected oldest kept line to be \"line 11\", got %q", lines[0])
	}
	if lines[matrixTailLines-1] != "line 50" {
		t.Errorf("expected last line \"line 50\", got %q", lines[matrixTailLines-1])
	}
}

func TestTailStderrMissingFile(t *testing.T) {
	if got := tailStderr(t.TempDir(), "ghost"); got != "" {
		t.Errorf("missing file should return \"\", got %q", got)
	}
	if got := tailStderr("", "x"); got != "" {
		t.Errorf("empty runDir should return \"\", got %q", got)
	}
}

func TestSanitizeForFSMatchesEvidence(t *testing.T) {
	cases := map[string]string{
		"plain":       "plain",
		"with space":  "with_space",
		"with/slash":  "with_slash",
		"with\\slash": "with_slash",
		"":            "",
	}
	for in, want := range cases {
		if got := sanitizeForFS(in); got != want {
			t.Errorf("sanitizeForFS(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestIsMatrixFormat(t *testing.T) {
	yes := []string{"matrix", "Matrix", "MATRIX", " matrix "}
	for _, s := range yes {
		if !isMatrixFormat(s) {
			t.Errorf("isMatrixFormat(%q) should be true", s)
		}
	}
	no := []string{"", "json", "text", "matrix2"}
	for _, s := range no {
		if isMatrixFormat(s) {
			t.Errorf("isMatrixFormat(%q) should be false", s)
		}
	}
}

func itoaSmall(i int) string {
	if i == 0 {
		return "0"
	}
	out := ""
	for i > 0 {
		out = string(rune('0'+i%10)) + out
		i /= 10
	}
	return out
}
