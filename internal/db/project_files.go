package db

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
)

func ProjectScopedPathName(projectID string) string {
	if isSafeProjectID(projectID) {
		return projectID
	}
	sum := sha256.Sum256([]byte(projectID))
	return "project_" + hex.EncodeToString(sum[:])
}

func ProjectScopedJSONFileName(projectID string) string {
	return ProjectScopedPathName(projectID) + ".json"
}

func RuntimeDescriptorPath(projectID string) string {
	return filepath.Join(RuntimeDir(), ProjectScopedJSONFileName(projectID))
}

func isSafeProjectID(projectID string) bool {
	if projectID == "" || len(projectID) > 128 {
		return false
	}
	for _, r := range projectID {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}
