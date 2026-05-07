package store

import (
	"fmt"
	"net"
)

// Pool is a named IP address range within the WG_SUBNET.
// Pools constrain IP allocation so that peers assigned to a pool
// receive addresses from that range.
type Pool struct {
	Name    string `json:"name"`
	StartIP net.IP `json:"start"`
	EndIP   net.IP `json:"end"`
}

// NewPool creates a validated Pool.  It checks that:
//   - StartIP and EndIP are valid IPv4 addresses
//   - StartIP <= EndIP
//   - The entire range lies within the given subnet
//   - The range does not include the server IP (subnet's .1 address)
func NewPool(name string, startIP, endIP net.IP, subnet *net.IPNet) (*Pool, error) {
	if name == "" {
		return nil, fmt.Errorf("pool name must not be empty")
	}

	s4 := startIP.To4()
	if s4 == nil {
		return nil, fmt.Errorf("pool %q: start IP %s is not a valid IPv4 address", name, startIP)
	}
	e4 := endIP.To4()
	if e4 == nil {
		return nil, fmt.Errorf("pool %q: end IP %s is not a valid IPv4 address", name, endIP)
	}

	// Start must be <= end.
	if ipToUint32(s4) > ipToUint32(e4) {
		return nil, fmt.Errorf("pool %q: start IP %s is after end IP %s", name, startIP, endIP)
	}

	// Entire range must be within the subnet.
	if !subnet.Contains(s4) {
		return nil, fmt.Errorf("pool %q: start IP %s is outside subnet %s", name, startIP, subnet)
	}
	if !subnet.Contains(e4) {
		return nil, fmt.Errorf("pool %q: end IP %s is outside subnet %s", name, endIP, subnet)
	}

	// The pool must not include the server IP (subnet's .1 address).
	// The server IP is network address + 1.
	if len(subnet.IP.To4()) != 4 {
		return nil, fmt.Errorf("subnet %s is not IPv4", subnet)
	}
	netIP := subnet.IP.To4()
	serverIP := net.IPv4(netIP[0], netIP[1], netIP[2], netIP[3]+1)

	p := &Pool{Name: name, StartIP: s4, EndIP: e4}
	if p.Contains(serverIP) {
		return nil, fmt.Errorf("pool %q: range %s-%s includes server IP %s", name, startIP, endIP, serverIP)
	}

	return p, nil
}

// Contains reports whether ip is within the pool's range (inclusive).
func (p *Pool) Contains(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	start := ipToUint32(p.StartIP.To4())
	end := ipToUint32(p.EndIP.To4())
	v := ipToUint32(ip4)
	return v >= start && v <= end
}

// ContainsStr is a convenience wrapper that parses a string IP.
func (p *Pool) ContainsStr(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return p.Contains(ip)
}

// rangeOverlap checks whether two uint32 ranges [aStart, aEnd] and
// [bStart, bEnd] share any addresses.
func rangeOverlap(aStart, aEnd, bStart, bEnd uint32) bool {
	return aStart <= bEnd && bStart <= aEnd
}

// ParsePools parses pool definitions from a config map (e.g.
// {"CLIENTS": "10.0.0.2-10.0.0.100"}) and returns validated Pool
// objects.  It also checks that no two pools overlap.
func ParsePools(subnetCIDR string, rawPools map[string]string) (map[string]*Pool, error) {
	_, subnet, err := net.ParseCIDR(subnetCIDR)
	if err != nil {
		return nil, fmt.Errorf("invalid subnet %q: %w", subnetCIDR, err)
	}

	pools := make(map[string]*Pool, len(rawPools))
	var ranges []struct {
		Name  string
		Start uint32
		End   uint32
	}

	for name, def := range rawPools {
		dash := -1
		for i, c := range def {
			if c == '-' {
				dash = i
				break
			}
		}
		if dash < 1 {
			return nil, fmt.Errorf("pool %q: invalid range format %q (expected startIP-endIP)", name, def)
		}
		startStr := def[:dash]
		endStr := def[dash+1:]

		startIP := net.ParseIP(startStr)
		if startIP == nil {
			return nil, fmt.Errorf("pool %q: invalid start IP %q", name, startStr)
		}
		endIP := net.ParseIP(endStr)
		if endIP == nil {
			return nil, fmt.Errorf("pool %q: invalid end IP %q", name, endStr)
		}

		pool, err := NewPool(name, startIP, endIP, subnet)
		if err != nil {
			return nil, err
		}
		pools[name] = pool

		ranges = append(ranges, struct {
			Name  string
			Start uint32
			End   uint32
		}{
			Name:  name,
			Start: ipToUint32(pool.StartIP),
			End:   ipToUint32(pool.EndIP),
		})
	}

	// Check for overlapping pools.
	for i := 0; i < len(ranges); i++ {
		for j := i + 1; j < len(ranges); j++ {
			if rangeOverlap(ranges[i].Start, ranges[i].End, ranges[j].Start, ranges[j].End) {
				return nil, fmt.Errorf("pools %q and %q overlap: %s-%s vs %s-%s",
					ranges[i].Name, ranges[j].Name,
					pools[ranges[i].Name].StartIP, pools[ranges[i].Name].EndIP,
					pools[ranges[j].Name].StartIP, pools[ranges[j].Name].EndIP,
				)
			}
		}
	}

	return pools, nil
}

// ipToUint32 converts an IPv4 address to a uint32 for comparison.
func ipToUint32(ip net.IP) uint32 {
	ip4 := ip.To4()
	if ip4 == nil {
		return 0
	}
	return uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3])
}
