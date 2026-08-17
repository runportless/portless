package database

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/portless-run/portless/portless-daemon/networking"
)

const (
	allocationCount = networking.EndpointPoolSize
)

type NetworkAllocation struct {
	Kind       string
	Source     string
	Target     string
	Protocol   model.Protocol
	DNSName    string
	ListenIP   string
	ListenPort int
	CreatedAt  time.Time
}

func (allocation NetworkAllocation) Address() string {
	return net.JoinHostPort(allocation.ListenIP, fmt.Sprintf("%d", allocation.ListenPort))
}

func (allocation NetworkAllocation) Endpoint(kind model.EndpointKind) model.Endpoint {
	endpoint := model.Endpoint{
		Kind: kind, Protocol: allocation.Protocol, Host: allocation.DNSName,
		Port: allocation.ListenPort, URL: networking.EndpointURL(allocation.Protocol, allocation.DNSName, allocation.ListenPort),
	}
	if kind == model.EndpointConnection {
		endpoint.Address = allocation.Address()
	}
	return endpoint
}

func (s *Store) SyncNetworkAllocations(ctx context.Context, selector string, specs []networking.AllocationSpec) error {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := syncNetworkAllocationsTx(ctx, tx, key, specs); err != nil {
		return err
	}
	return tx.Commit()
}

func syncNetworkAllocationsTx(ctx context.Context, tx *sql.Tx, environmentKey string, specs []networking.AllocationSpec) error {
	desired := make(map[string]networking.AllocationSpec, len(specs))
	for _, spec := range specs {
		if spec.Kind != networking.AllocationPublic && spec.Kind != networking.AllocationConnection {
			return fmt.Errorf("invalid endpoint allocation kind %q", spec.Kind)
		}
		if spec.Target == "" || spec.Port < 1 || spec.Port > 65535 {
			return fmt.Errorf("invalid endpoint allocation for %s", spec.Target)
		}
		if err := networking.ValidateDNSName(spec.DNSName); err != nil {
			return fmt.Errorf("invalid endpoint name %s: %w", spec.DNSName, err)
		}
		desired[allocationKey(spec.Kind, spec.Source, spec.Target, spec.Protocol)] = spec
	}

	rows, err := tx.QueryContext(ctx, `
SELECT endpoint_kind, source_name, target_name, protocol, dns_name, listen_ip, listen_port, created_at
FROM network_allocations WHERE environment_key = ?`, environmentKey)
	if err != nil {
		return err
	}
	existing := make(map[string]NetworkAllocation)
	for rows.Next() {
		var item NetworkAllocation
		var protocol, created string
		if err := rows.Scan(&item.Kind, &item.Source, &item.Target, &protocol, &item.DNSName, &item.ListenIP, &item.ListenPort, &created); err != nil {
			rows.Close()
			return err
		}
		item.Protocol = model.Protocol(protocol)
		item.CreatedAt = parseTime(created)
		existing[allocationKey(item.Kind, item.Source, item.Target, item.Protocol)] = item
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for key, allocation := range existing {
		if _, keep := desired[key]; keep {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
DELETE FROM network_allocations
WHERE environment_key = ? AND endpoint_kind = ? AND source_name = ? COLLATE NOCASE
  AND target_name = ? COLLATE NOCASE AND protocol = ?`,
			environmentKey, allocation.Kind, allocation.Source, allocation.Target, allocation.Protocol); err != nil {
			return err
		}
		delete(existing, key)
	}

	for key, spec := range desired {
		if allocation, ok := existing[key]; ok {
			if allocation.DNSName == spec.DNSName && allocation.ListenPort == spec.Port {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
UPDATE network_allocations SET dns_name = ?, listen_port = ?
WHERE environment_key = ? AND endpoint_kind = ? AND source_name = ? COLLATE NOCASE
  AND target_name = ? COLLATE NOCASE AND protocol = ?`,
				spec.DNSName, spec.Port, environmentKey, spec.Kind, spec.Source, spec.Target, spec.Protocol); err != nil {
				return fmt.Errorf("update endpoint allocation %s: %w", spec.DNSName, err)
			}
			continue
		}
		seed := allocationKey(environmentKey, spec.Kind, spec.Source, spec.Target, spec.Protocol)
		start := allocationStart(seed)
		allocated := false
		for offset := 0; offset < allocationCount; offset++ {
			ip := allocationIP((start + offset) % allocationCount)
			result, insertErr := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO network_allocations(
  environment_key, endpoint_kind, source_name, target_name, protocol, dns_name, listen_ip, listen_port, created_at
) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				environmentKey, spec.Kind, spec.Source, spec.Target, spec.Protocol,
				spec.DNSName, ip, spec.Port, nowText())
			if insertErr != nil {
				return fmt.Errorf("allocate endpoint %s: %w", spec.DNSName, insertErr)
			}
			changed, changedErr := result.RowsAffected()
			if changedErr != nil {
				return changedErr
			}
			if changed == 1 {
				allocated = true
				break
			}
		}
		if !allocated {
			return fmt.Errorf("Portless loopback endpoint pool is exhausted")
		}
	}
	return nil
}

func (s *Store) NetworkAllocations(ctx context.Context, selector string) ([]NetworkAllocation, error) {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT endpoint_kind, source_name, target_name, protocol, dns_name, listen_ip, listen_port, created_at
FROM network_allocations WHERE environment_key = ?
ORDER BY endpoint_kind, target_name COLLATE NOCASE, source_name COLLATE NOCASE`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]NetworkAllocation, 0)
	for rows.Next() {
		var item NetworkAllocation
		var protocol, created string
		if err := rows.Scan(&item.Kind, &item.Source, &item.Target, &protocol, &item.DNSName, &item.ListenIP, &item.ListenPort, &created); err != nil {
			return nil, err
		}
		item.Protocol = model.Protocol(protocol)
		item.CreatedAt = parseTime(created)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) NetworkAllocation(ctx context.Context, selector, kind, source, target string, protocol model.Protocol) (NetworkAllocation, error) {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return NetworkAllocation{}, err
	}
	return networkAllocationByKey(ctx, s.db, key, kind, source, target, protocol)
}

func networkAllocationByKey(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, environmentKey, kind, source, target string, protocol model.Protocol) (NetworkAllocation, error) {
	var result NetworkAllocation
	var encodedProtocol, created string
	err := query.QueryRowContext(ctx, `
SELECT endpoint_kind, source_name, target_name, protocol, dns_name, listen_ip, listen_port, created_at
FROM network_allocations
WHERE environment_key = ? AND endpoint_kind = ? AND source_name = ? COLLATE NOCASE
  AND target_name = ? COLLATE NOCASE AND protocol = ?`,
		environmentKey, kind, source, target, protocol).Scan(
		&result.Kind, &result.Source, &result.Target, &encodedProtocol, &result.DNSName,
		&result.ListenIP, &result.ListenPort, &created,
	)
	if err != nil {
		return NetworkAllocation{}, mapSQLError(err)
	}
	result.Protocol = model.Protocol(encodedProtocol)
	result.CreatedAt = parseTime(created)
	return result, nil
}

// ResolveNetworkName is used by the local authoritative DNS server. It never
// searches or forwards outside the Portless allocation table.
func (s *Store) ResolveNetworkName(ctx context.Context, name string) (netip.Addr, bool, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	var encoded string
	err := s.db.QueryRowContext(ctx, `SELECT listen_ip FROM network_allocations WHERE dns_name = ? COLLATE NOCASE`, name).Scan(&encoded)
	if err != nil {
		if err == sql.ErrNoRows {
			return netip.Addr{}, false, nil
		}
		return netip.Addr{}, false, err
	}
	address, err := netip.ParseAddr(encoded)
	if err != nil || !address.Is4() || !address.IsLoopback() {
		return netip.Addr{}, false, fmt.Errorf("stored endpoint address %q is invalid", encoded)
	}
	return address, true, nil
}

func allocationKey(parts ...any) string {
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		values = append(values, strings.ToLower(fmt.Sprint(part)))
	}
	return strings.Join(values, "\x00")
}

func allocationStart(seed string) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(seed))
	return int(hash.Sum32() % allocationCount)
}

func allocationIP(index int) string {
	return networking.EndpointLoopbackIP(index)
}
