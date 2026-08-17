package dns

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/portless-run/portless/portless-daemon/networking"
)

const (
	TypeA    uint16 = 1
	TypeAAAA uint16 = 28
	ClassIN  uint16 = 1

	ResponseSuccess       uint16 = 0
	ResponseFormatError   uint16 = 1
	ResponseServerFailure uint16 = 2
	ResponseNameError     uint16 = 3
	ResponseRefused       uint16 = 5

	DefaultTTL = 5
	MaxMessage = 4096
)

var HealthAddress = netip.MustParseAddr("127.77.0.1")

type Resolver interface {
	ResolveNetworkName(context.Context, string) (netip.Addr, bool, error)
}

type question struct {
	name  string
	kind  uint16
	class uint16
	end   int
}

func Response(ctx context.Context, resolver Resolver, query []byte) []byte {
	if len(query) < 12 || len(query) > MaxMessage {
		return errorResponse(query, ResponseFormatError)
	}
	flags := binary.BigEndian.Uint16(query[2:4])
	if flags&0x8000 != 0 || flags&0x7800 != 0 || binary.BigEndian.Uint16(query[4:6]) != 1 {
		return errorResponse(query, ResponseFormatError)
	}
	parsed, err := parseQuestion(query)
	if err != nil {
		return errorResponse(query, ResponseFormatError)
	}
	if parsed.class != ClassIN {
		return answer(query, parsed, netip.Addr{}, false, ResponseRefused)
	}
	name := strings.TrimSuffix(strings.ToLower(parsed.name), ".")
	if name != networking.DNSZone && !strings.HasSuffix(name, "."+networking.DNSZone) {
		return answer(query, parsed, netip.Addr{}, false, ResponseRefused)
	}
	address, found, resolveErr := netip.Addr{}, false, error(nil)
	if name == networking.DNSZone || name == "dns."+networking.DNSZone {
		address, found = HealthAddress, true
	} else if resolver != nil {
		address, found, resolveErr = resolver.ResolveNetworkName(ctx, name)
	}
	if resolveErr != nil {
		return answer(query, parsed, netip.Addr{}, false, ResponseServerFailure)
	}
	if !found {
		return answer(query, parsed, netip.Addr{}, false, ResponseNameError)
	}
	if parsed.kind != TypeA || !address.Is4() {
		// The name exists, but this first release intentionally has no AAAA (or
		// other record type). Return NODATA rather than NXDOMAIN.
		return answer(query, parsed, netip.Addr{}, false, ResponseSuccess)
	}
	return answer(query, parsed, address, true, ResponseSuccess)
}

func ServerFailure(query []byte) []byte {
	if parsed, err := parseQuestion(query); err == nil {
		return answer(query, parsed, netip.Addr{}, false, ResponseServerFailure)
	}
	return errorResponse(query, ResponseServerFailure)
}

func Query(name string, kind uint16, id uint16) ([]byte, error) {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	if err := networking.ValidateDNSName(strings.ToLower(name)); err != nil {
		return nil, err
	}
	result := make([]byte, 12)
	binary.BigEndian.PutUint16(result[0:2], id)
	binary.BigEndian.PutUint16(result[2:4], 0x0100) // recursion desired is harmless and is not honored.
	binary.BigEndian.PutUint16(result[4:6], 1)
	for _, label := range strings.Split(name, ".") {
		result = append(result, byte(len(label)))
		result = append(result, label...)
	}
	result = append(result, 0, 0, 0, 0, 0)
	binary.BigEndian.PutUint16(result[len(result)-4:len(result)-2], kind)
	binary.BigEndian.PutUint16(result[len(result)-2:], ClassIN)
	return result, nil
}

func ParseAResponse(message []byte, expectedID uint16) (netip.Addr, uint16, error) {
	if len(message) < 12 || binary.BigEndian.Uint16(message[0:2]) != expectedID || message[2]&0x80 == 0 {
		return netip.Addr{}, 0, errors.New("invalid DNS response")
	}
	rcode := binary.BigEndian.Uint16(message[2:4]) & 0x000f
	parsed, err := parseQuestion(message)
	if err != nil {
		return netip.Addr{}, rcode, err
	}
	answers := binary.BigEndian.Uint16(message[6:8])
	if answers == 0 {
		return netip.Addr{}, rcode, nil
	}
	offset := parsed.end
	if offset+12 > len(message) {
		return netip.Addr{}, rcode, errors.New("truncated DNS answer")
	}
	if message[offset]&0xc0 == 0xc0 {
		offset += 2
	} else {
		_, next, decodeErr := decodeName(message, offset)
		if decodeErr != nil {
			return netip.Addr{}, rcode, decodeErr
		}
		offset = next
	}
	if offset+10 > len(message) {
		return netip.Addr{}, rcode, errors.New("truncated DNS record")
	}
	kind := binary.BigEndian.Uint16(message[offset : offset+2])
	length := int(binary.BigEndian.Uint16(message[offset+8 : offset+10]))
	offset += 10
	if kind != TypeA || length != 4 || offset+4 > len(message) {
		return netip.Addr{}, rcode, errors.New("DNS response did not contain an IPv4 answer")
	}
	return netip.AddrFrom4([4]byte(message[offset : offset+4])), rcode, nil
}

func parseQuestion(message []byte) (question, error) {
	if len(message) < 12 {
		return question{}, errors.New("DNS header is truncated")
	}
	name, offset, err := decodeName(message, 12)
	if err != nil {
		return question{}, err
	}
	if offset+4 > len(message) {
		return question{}, errors.New("DNS question is truncated")
	}
	return question{
		name: name, kind: binary.BigEndian.Uint16(message[offset : offset+2]),
		class: binary.BigEndian.Uint16(message[offset+2 : offset+4]), end: offset + 4,
	}, nil
}

func decodeName(message []byte, offset int) (string, int, error) {
	labels := make([]string, 0, 6)
	for {
		if offset >= len(message) {
			return "", 0, errors.New("DNS name is truncated")
		}
		length := int(message[offset])
		offset++
		if length == 0 {
			break
		}
		if length&0xc0 != 0 || length > 63 || offset+length > len(message) {
			return "", 0, errors.New("DNS question name is malformed")
		}
		labels = append(labels, string(message[offset:offset+length]))
		offset += length
		if len(strings.Join(labels, ".")) > 253 {
			return "", 0, errors.New("DNS name is too long")
		}
	}
	return strings.Join(labels, "."), offset, nil
}

func answer(query []byte, parsed question, address netip.Addr, includeAddress bool, rcode uint16) []byte {
	result := make([]byte, 12, parsed.end+16)
	copy(result[0:2], query[0:2])
	queryFlags := binary.BigEndian.Uint16(query[2:4])
	binary.BigEndian.PutUint16(result[2:4], 0x8400|(queryFlags&0x0100)|(rcode&0x000f)) // response + authoritative
	binary.BigEndian.PutUint16(result[4:6], 1)
	if includeAddress {
		binary.BigEndian.PutUint16(result[6:8], 1)
	}
	result = append(result, query[12:parsed.end]...)
	if !includeAddress {
		return result
	}
	result = append(result, 0xc0, 0x0c)
	result = binary.BigEndian.AppendUint16(result, TypeA)
	result = binary.BigEndian.AppendUint16(result, ClassIN)
	result = binary.BigEndian.AppendUint32(result, DefaultTTL)
	result = binary.BigEndian.AppendUint16(result, 4)
	encoded := address.As4()
	result = append(result, encoded[:]...)
	return result
}

func errorResponse(query []byte, rcode uint16) []byte {
	result := make([]byte, 12)
	if len(query) >= 2 {
		copy(result[0:2], query[0:2])
	}
	flags := uint16(0x8000 | 0x0400 | (rcode & 0x000f))
	if len(query) >= 4 {
		flags |= binary.BigEndian.Uint16(query[2:4]) & 0x0100
	}
	binary.BigEndian.PutUint16(result[2:4], flags)
	return result
}

func ResponseCode(message []byte) (uint16, error) {
	if len(message) < 4 {
		return 0, fmt.Errorf("DNS response is truncated")
	}
	return binary.BigEndian.Uint16(message[2:4]) & 0x000f, nil
}
