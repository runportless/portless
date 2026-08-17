package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/portless-run/portless/portless-daemon/api/contract"
	"github.com/portless-run/portless/portless-daemon/database"
)

func TestWriteErrorClassifiesIdempotencyConflicts(t *testing.T) {
	recorder := httptest.NewRecorder()
	new(Server).writeError(recorder, database.ErrIdempotencyConflict, nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
	var envelope contract.ErrorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("code = %q, want IDEMPOTENCY_CONFLICT", envelope.Error.Code)
	}
}
