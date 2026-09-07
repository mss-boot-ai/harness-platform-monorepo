package store

import "testing"

func TestVerifyAllSchemaRejectsMalformedBaseSecurityIndexes(t *testing.T) {
	tests := []struct {
		name      string
		model     any
		indexName string
		createSQL string
	}{
		{
			name: "endpoint signing non unique", model: new(endpointRow), indexName: "ux_harness_endpoint_owner_sign",
			createSQL: "CREATE INDEX ux_harness_endpoint_owner_sign ON harness_endpoints(owner_user_id, signing_jkt)",
		},
		{
			name: "endpoint signing partial", model: new(endpointRow), indexName: "ux_harness_endpoint_owner_sign",
			createSQL: "CREATE UNIQUE INDEX ux_harness_endpoint_owner_sign ON harness_endpoints(owner_user_id, signing_jkt) WHERE owner_user_id <> ''",
		},
		{
			name: "endpoint kem wrong order", model: new(endpointRow), indexName: "ux_harness_endpoint_owner_kem",
			createSQL: "CREATE UNIQUE INDEX ux_harness_endpoint_owner_kem ON harness_endpoints(kem_jkt, owner_user_id)",
		},
		{
			name: "frame sequence missing column", model: new(frameRow), indexName: "ux_harness_frame_sequence",
			createSQL: "CREATE UNIQUE INDEX ux_harness_frame_sequence ON harness_frames(session_id, key_generation, sender_endpoint_id)",
		},
		{
			name: "credential token wrong column", model: new(credentialRow), indexName: "ux_harness_credential_token_hash",
			createSQL: "CREATE UNIQUE INDEX ux_harness_credential_token_hash ON harness_endpoint_credentials(endpoint_id)",
		},
		{
			name: "ticket token non unique", model: new(ticketRow), indexName: "ux_harness_ticket_token_hash",
			createSQL: "CREATE INDEX ux_harness_ticket_token_hash ON harness_ws_tickets(token_hash)",
		},
		{
			name: "HC challenge non unique", model: new(hcRegistrationChallengeRow), indexName: "ux_harness_hc_challenge_hash",
			createSQL: "CREATE INDEX ux_harness_hc_challenge_hash ON harness_hc_registration_challenges(challenge_hash)",
		},
		{
			name: "refresh token wrong column", model: new(refreshCredentialRow), indexName: "ux_harness_refresh_token_hash",
			createSQL: "CREATE UNIQUE INDEX ux_harness_refresh_token_hash ON harness_refresh_credentials(endpoint_id)",
		},
		{
			name: "control outbox non unique", model: new(controlOutboxRow), indexName: "ux_harness_control_outbox_correlation",
			createSQL: "CREATE INDEX ux_harness_control_outbox_correlation ON harness_control_outbox(kind, correlation_id, recipient_endpoint_id)",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			persistence := newTestStore(t)
			if err := persistence.db.Migrator().DropIndex(test.model, test.indexName); err != nil {
				t.Fatalf("DropIndex(%s): %v", test.indexName, err)
			}
			if err := persistence.db.Exec(test.createSQL).Error; err != nil {
				t.Fatalf("create malformed index: %v", err)
			}
			if err := VerifyAllSchema(persistence.db); err == nil {
				t.Fatalf("VerifyAllSchema accepted malformed index %s", test.indexName)
			}
			if err := CreateReliabilitySchema(persistence.db); err != nil {
				t.Fatalf("forward reliability migration did not repair %s: %v", test.indexName, err)
			}
			if err := VerifyAllSchema(persistence.db); err != nil {
				t.Fatalf("VerifyAllSchema after repair: %v", err)
			}
		})
	}
}
