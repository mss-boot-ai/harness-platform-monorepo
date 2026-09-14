package domain

// Only fixed local admission outcomes are public metadata, never runtime text.
func PublicStartupFailureCode(code string) string {
	switch code {
	case "RESOURCE_BUSY", "RUNTIME_BUSY", "WORKSPACE_BUSY", "AGENT_START_FAILED", "AGENT_START_EXPIRED", "SESSION_ALREADY_EXISTS", "RUNTIME_NOT_ALLOWED", "WORKSPACE_NOT_ALLOWED", "LOCAL_POLICY_REJECTED":
		return code
	default:
		return "AGENT_START_FAILED"
	}
}
