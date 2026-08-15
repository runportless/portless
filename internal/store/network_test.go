package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/networking"
)

func TestNetworkAllocationsAreStableAndDistinct(t *testing.T) {
	controlStore, err := Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controlStore.Close() })
	ctx := context.Background()
	definition := model.ProjectModel{
		SuggestedName: "store", PrimaryService: "orders",
		Services: []model.ServiceDefinition{
			{Name: "orders", Kind: model.ServiceProcess},
			{Name: "postgres", Kind: model.ServiceContainer, Template: "postgres"},
		},
		Connections: []model.Connection{{Source: "orders", Target: "postgres", Protocol: model.ProtocolPostgres}},
	}
	if _, err := controlStore.CreateProject(ctx, "store", definition, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "store", "local", definition, nil, nil); err != nil {
		t.Fatal(err)
	}
	specs, err := networking.AllocationSpecs("store", "local", definition)
	if err != nil {
		t.Fatal(err)
	}
	if err := controlStore.SyncNetworkAllocations(ctx, "store/local", specs); err != nil {
		t.Fatal(err)
	}
	before, err := controlStore.NetworkAllocations(ctx, "store/local")
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 2 || before[0].ListenIP == before[1].ListenIP {
		t.Fatalf("unexpected allocations: %#v", before)
	}
	if err := controlStore.SyncNetworkAllocations(ctx, "store/local", specs); err != nil {
		t.Fatal(err)
	}
	after, _ := controlStore.NetworkAllocations(ctx, "store/local")
	if before[0].ListenIP != after[0].ListenIP || before[1].ListenIP != after[1].ListenIP {
		t.Fatalf("allocations changed: before=%#v after=%#v", before, after)
	}
	resolved, found, err := controlStore.ResolveNetworkName(ctx, "postgres.local.store.portless.test.")
	if err != nil || !found || resolved.String() == "" {
		t.Fatalf("resolve result address=%s found=%v err=%v", resolved, found, err)
	}
}

func TestNetworkAllocationsFollowProjectRenameWithoutChangingAddresses(t *testing.T) {
	controlStore, err := Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controlStore.Close() })
	ctx := context.Background()
	definition := model.ProjectModel{
		SuggestedName: "store", PrimaryService: "orders",
		Services: []model.ServiceDefinition{
			{Name: "orders", Kind: model.ServiceProcess},
			{Name: "postgres", Kind: model.ServiceContainer, Template: "postgres"},
		},
		Connections: []model.Connection{{Source: "orders", Target: "postgres", Protocol: model.ProtocolPostgres}},
	}
	project, err := controlStore.CreateProject(ctx, "store", definition, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "store", "local", definition, nil, nil); err != nil {
		t.Fatal(err)
	}
	before, err := controlStore.NetworkAllocations(ctx, "store/local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.RenameProject(ctx, "store", "shop", project.Revision); err != nil {
		t.Fatal(err)
	}
	after, err := controlStore.NetworkAllocations(ctx, "shop/local")
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("allocation count changed: before=%#v after=%#v", before, after)
	}
	for index := range before {
		if before[index].ListenIP != after[index].ListenIP || !strings.Contains(after[index].DNSName, ".shop.portless.test") {
			t.Fatalf("allocation was not renamed in place: before=%#v after=%#v", before[index], after[index])
		}
	}
	if _, found, err := controlStore.ResolveNetworkName(ctx, "postgres.local.store.portless.test"); err != nil || found {
		t.Fatalf("old project endpoint still resolves: found=%t err=%v", found, err)
	}
}
