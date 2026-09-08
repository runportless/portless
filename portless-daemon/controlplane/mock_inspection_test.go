package controlplane

import (
	"reflect"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestEnvironmentListsIncludeMockActivity(t *testing.T) {
	for _, policy := range []model.MockUnmatchedRequests{model.MockUnmatchedReject, model.MockUnmatchedForward} {
		t.Run(string(policy), func(t *testing.T) {
			app, store := mockScenarioTestService(t)
			ctx := t.Context()
			if policy == model.MockUnmatchedForward {
				createTestPartialMockScenario(t, app, "active", "inventory", "payments")
			} else {
				createTestMockScenario(t, app, "active", "inventory", "payments")
			}
			createTestMockScenario(t, app, "inactive", "checkout")
			assertActivity := func(want *model.ServiceMock) {
				t.Helper()
				inspected, err := app.Environment(ctx, "store", "local")
				if err != nil {
					t.Fatal(err)
				}
				environments := []model.Environment{inspected}
				for _, project := range []string{"", "store"} {
					listed, err := app.Environments(ctx, project)
					if err != nil || len(listed) != 1 {
						t.Fatalf("list %q: environments=%d, err=%v", project, len(listed), err)
					}
					environments = append(environments, listed...)
				}
				for index, environment := range environments {
					for _, service := range environment.Services {
						expected := want
						if service.Name == "checkout" {
							expected = nil
						}
						if !reflect.DeepEqual(service.Mock, expected) {
							t.Errorf("snapshot %d service %s mock=%#v, want %#v", index, service.Name, service.Mock, expected)
						}
					}
				}
			}
			setEnabled := func(enabled bool) {
				t.Helper()
				op, err := app.SetMockScenarioEnabled(ctx, "store", "local", "active", enabled, "test", "")
				if err != nil {
					t.Fatal(err)
				}
				if op = waitForOperation(t, app, op); op.State != "succeeded" {
					t.Fatalf("set enabled=%t: %#v", enabled, op)
				}
			}
			assertActivity(nil)
			setEnabled(true)
			assertActivity(&model.ServiceMock{Scenario: "active", UnmatchedRequests: policy, State: model.MockScenarioEnabled})
			if policy == model.MockUnmatchedForward {
				if err := store.SetEnvironmentStatus(ctx, "store", "local", model.EnvironmentHealthy, ""); err != nil {
					t.Fatal(err)
				}
				app.proxy.RemovePartialMock("store/local", "active")
				assertActivity(&model.ServiceMock{Scenario: "active", UnmatchedRequests: policy, State: model.MockScenarioDegraded})
			}
			setEnabled(false)
			assertActivity(nil)
		})
	}
}
