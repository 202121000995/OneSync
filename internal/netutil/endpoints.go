package netutil

import (
	"errors"
	"fmt"
	"net"
	"sort"
)

// InterfaceAddress is the small subset of net.Addr needed for suggestions.
type InterfaceAddress interface {
	String() string
}

// EndpointSuggestions returns host:port candidates for non-loopback IPv4 addresses.
func EndpointSuggestions(port int, addresses []InterfaceAddress) ([]string, error) {
	if port < 1 || port > 65535 {
		return nil, errors.New("endpoint suggestion port is invalid")
	}
	seen := make(map[string]struct{})
	for _, address := range addresses {
		ip := addressIP(address)
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || !ip.IsPrivate() {
			continue
		}
		ipv4 := ip.To4()
		if ipv4 == nil {
			continue
		}
		seen[net.JoinHostPort(ipv4.String(), fmt.Sprint(port))] = struct{}{}
	}
	suggestions := make([]string, 0, len(seen))
	for suggestion := range seen {
		suggestions = append(suggestions, suggestion)
	}
	sort.Strings(suggestions)
	return suggestions, nil
}

// LocalEndpointSuggestions returns candidates from local network interfaces.
func LocalEndpointSuggestions(port int) ([]string, error) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("list local addresses: %w", err)
	}
	wrapped := make([]InterfaceAddress, 0, len(addresses))
	for _, address := range addresses {
		wrapped = append(wrapped, address)
	}
	return EndpointSuggestions(port, wrapped)
}

func addressIP(address InterfaceAddress) net.IP {
	switch value := address.(type) {
	case *net.IPNet:
		return value.IP
	case *net.IPAddr:
		return value.IP
	default:
		ip, _, err := net.ParseCIDR(value.String())
		if err == nil {
			return ip
		}
		return net.ParseIP(value.String())
	}
}
