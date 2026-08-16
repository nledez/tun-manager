// Package netctx answers a single question: which network am I on?
//
// Knowing the network is what lets a tunnel's group change with it: one that
// reaches a LAN is redundant while you are sitting on that LAN.
package netctx

import (
	"fmt"
	"net"
	"net/netip"
)

// Default is the context name used when no rule matches.
const Default = "default"

// Rule describes one recognisable network. An empty Interfaces list matches any
// interface.
type Rule struct {
	Name       string   `yaml:"name"`
	Interfaces []string `yaml:"interfaces"`
	CIDR       string   `yaml:"cidr"`
}

// Iface is an interface with its addresses.
type Iface struct {
	Name  string
	Addrs []netip.Prefix
}

// Lister enumerates the host interfaces. Tests inject a fake one.
type Lister func() ([]Iface, error)

// Context is the detected network, plus the evidence that led to it.
type Context struct {
	Name      string
	Interface string
	Address   string
}

// String renders the context for the TUI header, e.g. "office (en0 198.51.100.42)".
func (c Context) String() string {
	if c.Interface == "" {
		return c.Name
	}
	return fmt.Sprintf("%s (%s %s)", c.Name, c.Interface, c.Address)
}

// Detect returns the first rule matching a host address. Rule order is
// significant: the most specific rule must come first.
func Detect(rules []Rule, list Lister) (Context, error) {
	ifaces, err := list()
	if err != nil {
		return Context{}, fmt.Errorf("list interfaces: %w", err)
	}

	for _, rule := range rules {
		prefix, err := netip.ParsePrefix(rule.CIDR)
		if err != nil {
			return Context{}, fmt.Errorf("context %q: bad cidr %q: %w", rule.Name, rule.CIDR, err)
		}
		for _, iface := range ifaces {
			if !rule.matchesInterface(iface.Name) {
				continue
			}
			for _, addr := range iface.Addrs {
				if prefix.Contains(addr.Addr()) {
					return Context{
						Name:      rule.Name,
						Interface: iface.Name,
						Address:   addr.Addr().String(),
					}, nil
				}
			}
		}
	}
	return Context{Name: Default}, nil
}

func (r Rule) matchesInterface(name string) bool {
	if len(r.Interfaces) == 0 {
		return true
	}
	for _, want := range r.Interfaces {
		if want == name {
			return true
		}
	}
	return false
}

// System lists the host interfaces, skipping the ones that are down.
func System() ([]Iface, error) {
	list, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	out := make([]Iface, 0, len(list))
	for _, iface := range list {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			// A disappearing interface is not worth failing the whole detection.
			continue
		}
		entry := Iface{Name: iface.Name}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			prefix, ok := netip.AddrFromSlice(ipnet.IP)
			if !ok {
				continue
			}
			ones, _ := ipnet.Mask.Size()
			entry.Addrs = append(entry.Addrs, netip.PrefixFrom(prefix.Unmap(), ones))
		}
		out = append(out, entry)
	}
	return out, nil
}
