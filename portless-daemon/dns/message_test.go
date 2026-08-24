package dns

import (
	"context"
	"net/netip"
	"testing"
)

type resolverFunc func(context.Context, string) (netip.Addr, bool, error)

func (function resolverFunc) ResolveNetworkName(ctx context.Context, name string) (netip.Addr, bool, error) {
	return function(ctx, name)
}

func TestAuthoritativeAResponseAndNODATA(t *testing.T) {
	query, err := Query("postgres.local.store.portless.test", TypeA, 42)
	if err != nil {
		t.Fatal(err)
	}
	resolver := resolverFunc(func(_ context.Context, name string) (netip.Addr, bool, error) {
		if name != "postgres.local.store.portless.test" {
			t.Fatalf("unexpected name %q", name)
		}
		return netip.MustParseAddr("127.77.1.9"), true, nil
	})
	response := Response(context.Background(), resolver, query)
	address, rcode, err := ParseAResponse(response, 42)
	if err != nil || rcode != ResponseSuccess || address.String() != "127.77.1.9" {
		t.Fatalf("address=%s rcode=%d err=%v response=%x", address, rcode, err, response)
	}
	query, _ = Query("postgres.local.store.portless.test", TypeAAAA, 43)
	response = Response(context.Background(), resolver, query)
	if code, _ := ResponseCode(response); code != ResponseSuccess || response[7] != 0 {
		t.Fatalf("AAAA response should be NODATA: %x", response)
	}
}

func TestUnknownAndOutsideNamesAreNotForwarded(t *testing.T) {
	resolver := resolverFunc(func(context.Context, string) (netip.Addr, bool, error) {
		return netip.Addr{}, false, nil
	})
	query, _ := Query("missing.local.store.portless.test", TypeA, 1)
	if code, _ := ResponseCode(Response(context.Background(), resolver, query)); code != ResponseNameError {
		t.Fatalf("inside-zone unknown name returned code %d", code)
	}
	query, _ = Query("example.com", TypeA, 2)
	if code, _ := ResponseCode(Response(context.Background(), resolver, query)); code != ResponseRefused {
		t.Fatalf("outside-zone name returned code %d", code)
	}
}

func TestHealthNameDoesNotNeedApplicationState(t *testing.T) {
	query, _ := Query("portless.test", TypeA, 9)
	address, code, err := ParseAResponse(Response(context.Background(), nil, query), 9)
	if err != nil || code != 0 || address != HealthAddress {
		t.Fatalf("address=%s code=%d err=%v", address, code, err)
	}
}

func TestLocalhostNamesAlwaysResolveWithoutApplicationState(t *testing.T) {
	for index, name := range []string{"localhost", "portless.localhost", "checkout.local.store.localhost"} {
		query, err := Query(name, TypeA, uint16(20+index))
		if err != nil {
			t.Fatal(err)
		}
		response, handled := LocalhostResponse(query)
		if !handled {
			t.Fatalf("localhost query %q was not handled", name)
		}
		address, code, err := ParseAResponse(response, uint16(20+index))
		if err != nil || code != ResponseSuccess || address != LocalhostAddress {
			t.Fatalf("name=%s address=%s code=%d err=%v", name, address, code, err)
		}
		address, code, err = ParseAResponse(Response(context.Background(), nil, query), uint16(20+index))
		if err != nil || code != ResponseSuccess || address != LocalhostAddress {
			t.Fatalf("authoritative name=%s address=%s code=%d err=%v", name, address, code, err)
		}
	}

	query, _ := Query("checkout.local.store.localhost", TypeAAAA, 30)
	response, handled := LocalhostResponse(query)
	if !handled {
		t.Fatal("localhost AAAA query was not handled")
	}
	if code, _ := ResponseCode(response); code != ResponseSuccess || response[7] != 0 {
		t.Fatalf("localhost AAAA response should be NODATA: %x", response)
	}

	query, _ = Query("example.com", TypeA, 31)
	if _, handled := LocalhostResponse(query); handled {
		t.Fatal("outside-zone query was handled as localhost")
	}
}
