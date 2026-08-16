package netctx

import (
	"errors"
	"net/netip"
	"testing"
)

// Addresses come from the ranges reserved for documentation (RFC 5737).
var officeRules = []Rule{{
	Name:       "office",
	Interfaces: []string{"en0", "en10"},
	CIDR:       "198.51.100.0/24",
}}

func ifaces(pairs ...any) Lister {
	var out []Iface
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, Iface{
			Name:  pairs[i].(string),
			Addrs: []netip.Prefix{netip.MustParsePrefix(pairs[i+1].(string))},
		})
	}
	return func() ([]Iface, error) { return out, nil }
}

func TestDetectMatchesTheOfficeLAN(t *testing.T) {
	got, err := Detect(officeRules, ifaces("en0", "198.51.100.42/24"))
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if got.Name != "office" {
		t.Errorf("Name = %q, want %q", got.Name, "office")
	}
	if got.Interface != "en0" {
		t.Errorf("Interface = %q, want %q", got.Interface, "en0")
	}
	if got.Address != "198.51.100.42" {
		t.Errorf("Address = %q, want %q", got.Address, "198.51.100.42")
	}
}

func TestDetectMatchesSecondaryInterface(t *testing.T) {
	// A rule may name several interfaces: the wifi and a USB-C adapter, say.
	got, err := Detect(officeRules, ifaces("en0", "203.0.113.7/24", "en10", "198.51.100.9/24"))
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if got.Name != "office" {
		t.Errorf("Name = %q, want %q", got.Name, "office")
	}
	if got.Interface != "en10" {
		t.Errorf("Interface = %q, want %q", got.Interface, "en10")
	}
}

func TestDetectReturnsDefaultWhenAway(t *testing.T) {
	got, err := Detect(officeRules, ifaces("en0", "203.0.113.20/24"))
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if got.Name != Default {
		t.Errorf("Name = %q, want %q", got.Name, Default)
	}
}

func TestDetectIgnoresRightSubnetOnWrongInterface(t *testing.T) {
	// A VPN utun carrying the office subnet must not be mistaken for the LAN.
	got, err := Detect(officeRules, ifaces("utun4", "198.51.100.42/24"))
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if got.Name != Default {
		t.Errorf("Name = %q, want %q", got.Name, Default)
	}
}

func TestDetectHonoursRuleOrder(t *testing.T) {
	rules := []Rule{
		{Name: "campus", Interfaces: []string{"en0"}, CIDR: "198.51.0.0/16"},
		{Name: "office", Interfaces: []string{"en0"}, CIDR: "198.51.100.0/24"},
	}

	got, err := Detect(rules, ifaces("en0", "198.51.100.42/24"))
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if got.Name != "campus" {
		t.Errorf("Name = %q, want %q: the first matching rule wins", got.Name, "campus")
	}
}

func TestDetectMatchesAnyInterfaceWhenListEmpty(t *testing.T) {
	rules := []Rule{{Name: "lab", CIDR: "192.0.2.0/24"}}

	got, err := Detect(rules, ifaces("en5", "192.0.2.34/24"))
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if got.Name != "lab" {
		t.Errorf("Name = %q, want %q", got.Name, "lab")
	}
}

func TestDetectRejectsMalformedCIDR(t *testing.T) {
	rules := []Rule{{Name: "broken", CIDR: "not-a-cidr"}}

	if _, err := Detect(rules, ifaces("en0", "198.51.100.42/24")); err == nil {
		t.Fatal("Detect succeeded with a malformed CIDR, want error")
	}
}

func TestDetectPropagatesListerError(t *testing.T) {
	boom := errors.New("boom")
	lister := func() ([]Iface, error) { return nil, boom }

	if _, err := Detect(officeRules, lister); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
}

func TestContextStringShowsTheEvidence(t *testing.T) {
	c := Context{Name: "office", Interface: "en0", Address: "198.51.100.42"}

	if got := c.String(); got != "office (en0 198.51.100.42)" {
		t.Errorf("String = %q", got)
	}
}

func TestContextStringOfADefaultContextIsJustItsName(t *testing.T) {
	// No rule matched, so there is no interface to point at.
	c := Context{Name: Default}

	if got := c.String(); got != Default {
		t.Errorf("String = %q, want %q", got, Default)
	}
}

func TestSystemListsTheHostInterfaces(t *testing.T) {
	got, err := System()
	if err != nil {
		t.Fatalf("System: %v", err)
	}

	if len(got) == 0 {
		t.Fatal("System returned no interface, want at least the loopback")
	}

	var loopback bool
	for _, iface := range got {
		for _, addr := range iface.Addrs {
			if addr.Addr().IsLoopback() {
				loopback = true
			}
		}
	}
	if !loopback {
		t.Errorf("no loopback address among %+v", got)
	}
}

func TestSystemFeedsDetect(t *testing.T) {
	// The real lister and the fake one must be interchangeable.
	rules := []Rule{{Name: "loopback", CIDR: "127.0.0.0/8"}}

	got, err := Detect(rules, System)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if got.Name != "loopback" {
		t.Errorf("Name = %q, want %q", got.Name, "loopback")
	}
}
