package controlplane

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/model"
)

func TestMockPolicySwitchPreservesRoutesIdentityActivationAndProviders(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[active], func(t *testing.T) {
			app, store := mockScenarioTestService(t)
			ctx := t.Context()
			createTestMockScenario(t, app, "switchable", "inventory", "payments")
			original, _ := app.MockScenario(ctx, "store", "local", "switchable")
			before, _ := store.Environment(ctx, "store", "local")
			if active {
				op, err := app.SetMockScenarioEnabled(ctx, "store", "local", original.Name, true, "test", "enable")
				if err != nil {
					t.Fatal(err)
				}
				if op = waitForOperation(t, app, op); op.State != "succeeded" {
					t.Fatalf("enable: %#v", op)
				}
				if _, err := store.SetMockScenarioPolicy(ctx, "store", "local", original.Name, model.MockUnmatchedForward); !errors.Is(err, database.ErrConflict) {
					t.Fatalf("storage allowed policy changes with active ownership: %v", err)
				}
			}
			for _, policy := range []model.MockUnmatchedRequests{model.MockUnmatchedForward, model.MockUnmatchedReject} {
				op, err := app.SetMockScenarioPolicy(ctx, "store", "local", original.Name, policy, "test", string(policy))
				if err != nil {
					t.Fatal(err)
				}
				if op = waitForOperation(t, app, op); op.State != "succeeded" {
					t.Fatalf("switch: %#v", op)
				}
				updated, err := app.MockScenario(ctx, "store", "local", original.Name)
				if err != nil || updated.UnmatchedRequests != policy || !reflect.DeepEqual(updated.Routes, original.Routes) || !updated.CreatedAt.Equal(original.CreatedAt) || updated.Version.ModifiedAt == original.Version.ModifiedAt {
					t.Fatalf("scenario changed unexpectedly: %#v %v", updated, err)
				}
				if (updated.Activation.State == model.MockScenarioEnabled) != active {
					t.Fatalf("activation: %#v", updated.Activation)
				}
				preview, err := app.PreviewMock(ctx, "store", "local", original.Name, model.MockRequest{Service: "inventory", Method: "GET", Path: "/missing"}, nil, "")
				want := "rejected"
				if policy == model.MockUnmatchedForward {
					want = "forward"
				}
				if err != nil || preview.Outcome != want {
					t.Fatalf("preview: %#v %v", preview, err)
				}
				repeated, err := app.SetMockScenarioPolicy(ctx, "store", "local", original.Name, policy, "test", string(policy))
				if err != nil || repeated.Number != op.Number {
					t.Fatalf("retry: %#v %v", repeated, err)
				}
				after, _ := store.Environment(ctx, "store", "local")
				for _, binding := range before.Bindings {
					actual := bindingForEnvironment(after, binding.Service)
					if active && policy == model.MockUnmatchedReject && binding.Service != "checkout" {
						if actual.Provider != model.ProviderMock {
							t.Fatalf("full provider: %#v", actual)
						}
					} else if !sameProviderBinding(binding, actual) {
						t.Fatalf("provider changed: %#v", actual)
					}
				}
			}
			if active {
				op, err := app.SetMockScenarioEnabled(ctx, "store", "local", original.Name, false, "test", "disable")
				if err != nil {
					t.Fatal(err)
				}
				if op = waitForOperation(t, app, op); op.State != "succeeded" {
					t.Fatalf("restore: %#v", op)
				}
				after, _ := store.Environment(ctx, "store", "local")
				for _, binding := range before.Bindings {
					if !sameProviderBinding(binding, bindingForEnvironment(after, binding.Service)) {
						t.Fatalf("lost original provider: %s", binding.Service)
					}
				}
			}
		})
	}
}

func TestMockPolicyFailureRestoresPreviousModeAndOwnership(t *testing.T) {
	for _, originalPolicy := range []model.MockUnmatchedRequests{model.MockUnmatchedReject, model.MockUnmatchedForward} {
		t.Run(string(originalPolicy), func(t *testing.T) {
			app, store := mockScenarioTestService(t)
			ctx := t.Context()
			createTestMockScenario(t, app, "rollback", "inventory", "payments")
			if _, err := store.SetMockScenarioPolicy(ctx, "store", "local", "rollback", originalPolicy); err != nil {
				t.Fatal(err)
			}
			op, err := app.SetMockScenarioEnabled(ctx, "store", "local", "rollback", true, "test", "enable")
			if err != nil {
				t.Fatal(err)
			}
			if op = waitForOperation(t, app, op); op.State != "succeeded" {
				t.Fatalf("enable: %#v", op)
			}
			before, _ := app.MockScenario(ctx, "store", "local", "rollback")
			records, _ := store.MockScenarioActivations(ctx, "store", "local", "rollback")
			policy := model.MockUnmatchedForward
			trigger := `CREATE TRIGGER fail_policy_activation BEFORE INSERT ON mock_scenario_activations
WHEN NEW.unmatched_requests = 'forward' BEGIN SELECT RAISE(FAIL, 'injected activation failure'); END`
			if originalPolicy == model.MockUnmatchedForward {
				policy = model.MockUnmatchedReject
				trigger = `CREATE TRIGGER fail_policy_activation BEFORE INSERT ON environment_bindings
WHEN NEW.service_name = 'payments' AND NEW.provider = 'mock' BEGIN SELECT RAISE(FAIL, 'injected activation failure'); END`
			}
			if _, err := store.DB().ExecContext(ctx, trigger); err != nil {
				t.Fatal(err)
			}
			op, err = app.SetMockScenarioPolicy(ctx, "store", "local", "rollback", policy, "test", "switch")
			if err != nil {
				t.Fatal(err)
			}
			if op = waitForOperation(t, app, op); op.State != "failed" || !strings.Contains(op.Error, "injected activation failure") {
				t.Fatalf("switch: %#v", op)
			}
			after, _ := app.MockScenario(ctx, "store", "local", "rollback")
			if after.UnmatchedRequests != originalPolicy || after.Activation.State != model.MockScenarioEnabled || !reflect.DeepEqual(after.Routes, before.Routes) {
				t.Fatalf("rollback: %#v", after)
			}
			restored, _ := store.MockScenarioActivations(ctx, "store", "local", "rollback")
			if len(restored) != len(records) {
				t.Fatalf("lost restoration records: %#v", restored)
			}
			for i, record := range records {
				if !sameProviderBinding(record.PreviousBinding, restored[i].PreviousBinding) {
					t.Fatalf("lost saved provider: %#v", restored[i])
				}
			}
		})
	}
}

func TestMockPolicyValidationNoOpAndLifecycleConflicts(t *testing.T) {
	app, store := mockScenarioTestService(t)
	ctx := t.Context()
	createTestMockScenario(t, app, "scenario", "inventory")
	for _, policy := range []model.MockUnmatchedRequests{"", "invalid"} {
		if _, err := app.SetMockScenarioPolicy(ctx, "store", "local", "scenario", policy, "test", ""); err == nil {
			t.Fatalf("accepted policy %q", policy)
		}
	}
	before, _ := app.MockScenario(ctx, "store", "local", "scenario")
	op, err := app.SetMockScenarioPolicy(ctx, "store", "local", "scenario", model.MockUnmatchedReject, "test", "noop")
	if err != nil {
		t.Fatal(err)
	}
	if op = waitForOperation(t, app, op); op.State != "succeeded" {
		t.Fatal(op)
	}
	after, _ := app.MockScenario(ctx, "store", "local", "scenario")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("no-op changed the scenario")
	}
	if _, err := app.SetMockScenarioPolicy(ctx, "store", "local", "scenario", model.MockUnmatchedForward, "test", "noop"); !errors.Is(err, database.ErrIdempotencyConflict) {
		t.Fatalf("reused key: %v", err)
	}
	if err := store.SetEnvironmentStatus(ctx, "store", "local", model.EnvironmentStarting, ""); err != nil {
		t.Fatal(err)
	}
	op, err = app.SetMockScenarioPolicy(ctx, "store", "local", "scenario", model.MockUnmatchedForward, "test", "blocked")
	if err != nil {
		t.Fatal(err)
	}
	if op = waitForOperation(t, app, op); op.State != "failed" {
		t.Fatalf("lifecycle conflict: %#v", op)
	}
	after, _ = app.MockScenario(ctx, "store", "local", "scenario")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("blocked transition changed the scenario")
	}
}
