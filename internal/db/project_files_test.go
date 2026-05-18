package db

import (
	"strings"
	"testing"
)

func TestProjectScopedPathNamePreservesSafeProjectID(t *testing.T) {
	if got := ProjectScopedPathName("prj_abc-123"); got != "prj_abc-123" {
		t.Fatalf("ProjectScopedPathName = %q, want prj_abc-123", got)
	}
	if got := ProjectScopedJSONFileName("prj_abc-123"); got != "prj_abc-123.json" {
		t.Fatalf("ProjectScopedJSONFileName = %q, want prj_abc-123.json", got)
	}
}

func TestProjectScopedPathNameSanitizesUnsafeProjectID(t *testing.T) {
	got := ProjectScopedPathName("../../outside")
	if !strings.HasPrefix(got, "project_") || strings.Contains(got, "..") || strings.ContainsAny(got, `/\`) {
		t.Fatalf("ProjectScopedPathName = %q, want sanitized path segment", got)
	}
}

func TestProjectScopedPathNameHashesLongProjectID(t *testing.T) {
	got := ProjectScopedPathName(strings.Repeat("a", 129))
	if !strings.HasPrefix(got, "project_") || len(got) > 128 {
		t.Fatalf("ProjectScopedPathName = %q, want bounded hash segment", got)
	}
}
