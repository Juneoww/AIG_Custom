package websocket

import "testing"

func TestSkillsPlatformEngineType(t *testing.T) {
	actual, ok := platformEngineTaskType("skills_scan")
	if !ok || actual != "Skills-Scan" {
		t.Fatalf("Skills identity lost: %q, %v", actual, ok)
	}
}
