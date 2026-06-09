package netutil

import (
	"net"
	"testing"
)

func TestEndpointSuggestionsFiltersAndSortsIPv4(t *testing.T) {
	suggestions, err := EndpointSuggestions(7443, []InterfaceAddress{
		cidrAddress("127.0.0.1/8"),
		cidrAddress("192.168.1.10/24"),
		cidrAddress("10.0.0.7/24"),
		cidrAddress("192.168.1.10/24"),
		cidrAddress("198.18.0.1/15"),
		cidrAddress("fe80::1/64"),
	})
	if err != nil {
		t.Fatalf("EndpointSuggestions() error = %v", err)
	}
	want := []string{"10.0.0.7:7443", "192.168.1.10:7443"}
	if len(suggestions) != len(want) {
		t.Fatalf("suggestions = %v, want %v", suggestions, want)
	}
	for index := range want {
		if suggestions[index] != want[index] {
			t.Fatalf("suggestions = %v, want %v", suggestions, want)
		}
	}
}

func TestEndpointSuggestionsRejectsInvalidPort(t *testing.T) {
	if _, err := EndpointSuggestions(0, nil); err == nil {
		t.Fatal("EndpointSuggestions() accepted port 0")
	}
}

func cidrAddress(value string) *net.IPNet {
	ip, network, err := net.ParseCIDR(value)
	if err != nil {
		panic(err)
	}
	network.IP = ip
	return network
}
