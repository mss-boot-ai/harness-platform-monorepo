package enrollment

import (
	"testing"
	"time"
)

func TestServiceTimeUsesDatabasePrecision(t *testing.T) {
	value := time.Unix(1_800_000_000, 123_456_789).In(time.FixedZone("test", 8*60*60))
	got := (Service{Now: func() time.Time { return value }}).now()
	want := value.UTC().Truncate(time.Microsecond)
	if got != want {
		t.Fatalf("service time = %s, want %s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}
