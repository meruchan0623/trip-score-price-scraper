package tripcdp

import (
	"strings"
	"testing"
)

func TestCheckpointOutputPath(t *testing.T) {
	path := checkpointOutputPath("/tmp/out/BC_260708-0000.xlsx", "BC", 10)
	if !strings.Contains(path, "BC_partial_010.xlsx") {
		t.Fatalf("path = %q", path)
	}
}
