package protocol

import "testing"

func TestCapturePolicyKeepsLiveAndRecordingLimitsIndependent(t *testing.T) {
	policy := (Config{Policy: func() CapturePolicy {
		return CapturePolicy{Recording: "small", PersistPayloads: true, PayloadLimit: 16 << 10}
	}}).CapturePolicy()
	if policy.PayloadLimit != 16<<10 || policy.CaptureLimit() != LivePayloadLimit {
		t.Fatalf("small recording policy = %#v, capture limit = %d", policy, policy.CaptureLimit())
	}

	policy = (Config{Policy: func() CapturePolicy {
		return CapturePolicy{Recording: "large", PersistPayloads: true, PayloadLimit: 256 << 10}
	}}).CapturePolicy()
	if policy.PayloadLimit != 256<<10 || policy.CaptureLimit() != 256<<10 {
		t.Fatalf("large recording policy = %#v, capture limit = %d", policy, policy.CaptureLimit())
	}

	policy = (Config{Policy: func() CapturePolicy {
		return CapturePolicy{PayloadLimit: MaximumPayloadLimit + 1}
	}}).CapturePolicy()
	if policy.PayloadLimit != MaximumPayloadLimit || policy.CaptureLimit() != MaximumPayloadLimit {
		t.Fatalf("oversized recording policy = %#v, capture limit = %d", policy, policy.CaptureLimit())
	}
}
