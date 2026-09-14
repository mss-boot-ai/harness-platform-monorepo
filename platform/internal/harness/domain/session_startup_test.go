package domain

import "testing"

func TestStartupReasonDoesNotExposeArbitraryAgentText(t *testing.T) {
	if PublicStartupFailureCode("WORKSPACE_BUSY") != "WORKSPACE_BUSY" {
		t.Fatal("known local reason lost")
	}
	for _, raw := range []string{"PRIVATE_STARTUP_CANARY", "provider response", ""} {
		if PublicStartupFailureCode(raw) != "AGENT_START_FAILED" {
			t.Fatal("untrusted startup text exposed")
		}
	}
}
