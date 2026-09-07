package main

import "testing"

func TestGatewayDatabaseDialector(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		driver   string
		dsn      string
		wantName string
		wantErr  bool
	}{
		{name: "sqlite default", driver: "sqlite", dsn: "file:test.db", wantName: "sqlite"},
		{name: "postgres for TimescaleDB", driver: " POSTGRES ", dsn: "postgres://example.invalid/harness", wantName: "postgres"},
		{name: "missing DSN", driver: "postgres", wantErr: true},
		{name: "unsupported driver", driver: "mysql", dsn: "unused", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dialector, err := gatewayDatabaseDialector(test.driver, test.dsn)
			if test.wantErr {
				if err == nil {
					t.Fatal("gatewayDatabaseDialector() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("gatewayDatabaseDialector() error = %v", err)
			}
			if got := dialector.Name(); got != test.wantName {
				t.Fatalf("dialector.Name() = %q, want %q", got, test.wantName)
			}
		})
	}
}
