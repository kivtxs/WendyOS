package vpn

import (
	"net"
	"net/url"
	"strings"

	"github.com/miekg/dns"
	"github.com/wendylabsinc/wendy/go/internal/shared/meshid"
	"github.com/wendylabsinc/wendy/go/internal/shared/meshname"
)

func AnswerDNS(wire []byte, routes []Route) ([]byte, error) {
	var query dns.Msg
	if err := query.Unpack(wire); err != nil {
		return nil, err
	}
	reply := new(dns.Msg)
	reply.SetReply(&query)
	reply.Authoritative = true
	if query.Response || query.Opcode != dns.OpcodeQuery || len(query.Question) != 1 {
		reply.Rcode = dns.RcodeFormatError
		return reply.Pack()
	}
	q := query.Question[0]
	name := strings.ToLower(dns.Fqdn(q.Name))
	if q.Qclass != dns.ClassINET {
		reply.Rcode = dns.RcodeRefused
		return reply.Pack()
	}
	label, suffix, ok := deviceDNSLabel(name)
	if !ok {
		reply.Rcode = dns.RcodeRefused
		return reply.Pack()
	}
	for _, c := range q.Name {
		if c > 127 {
			reply.Rcode = dns.RcodeNameError
			return reply.Pack()
		}
	}
	identity := strings.HasPrefix(label, "device-") && !strings.Contains(label, ".")
	if suffix == meshname.Suffix {
		_, err := meshid.ParseDeviceUUID(strings.TrimPrefix(label, "device-"))
		identity = identity && err == nil
	}
	if !identity && suffix != meshname.Suffix {
		// Public compatibility zones also contain public services. Let macOS use
		// its system resolver for names this device resolver does not own.
		reply.Rcode = dns.RcodeRefused
		reply.Authoritative = false
		return reply.Pack()
	}
	var found *Route
	priority := 0
	for i := range routes {
		r := &routes[i]
		score := 0
		if name == strings.ToLower(dns.Fqdn(r.Host)) {
			score = 2
		}
		if identity {
			routeLabel, _, valid := deviceDNSLabel(strings.ToLower(dns.Fqdn(r.Host)))
			// The new namespace is identity-based. Numeric compatibility hostnames
			// must not be interpreted as device UUIDs there.
			_, uuidErr := meshid.ParseDeviceUUID(strings.TrimPrefix(label, "device-"))
			if valid && routeLabel == label && !strings.HasPrefix(r.Selector, "cloud://") && (suffix != meshname.Suffix || uuidErr == nil) && score == 0 {
				score = 1
			}
		} else if host, valid := meshname.Friendly(r.Name); valid && name == dns.Fqdn(host) && singleTenant(routes) {
			score = 1
		}
		if score > 0 {
			if score < priority {
				continue
			}
			if found != nil && score == priority && found.Selector != r.Selector {
				reply.Rcode = dns.RcodeNameError
				return reply.Pack()
			}
			found = r
			priority = score
		}
	}
	if found == nil {
		reply.Rcode = dns.RcodeNameError
		return reply.Pack()
	}
	if q.Qtype == dns.TypeA {
		reply.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 30}, A: net.ParseIP(found.Address)}}
	}
	return reply.Pack()
}

func deviceDNSLabel(name string) (string, string, bool) {
	// Match child namespaces before wendy.internal itself.
	for _, suffix := range []string{meshname.PreviousSuffix, "mesh.wendy.dev", meshname.LegacySuffix, meshname.Suffix} {
		if tail := "." + suffix + "."; strings.HasSuffix(name, tail) {
			return strings.TrimSuffix(name, tail), suffix, true
		}
	}
	return "", "", false
}

// Short friendly names have a meaning only within one enrolled tenant. A
// caller supplying routes from multiple tenants must use UUID identities.
func singleTenant(routes []Route) bool {
	tenant := ""
	for _, r := range routes {
		u, err := url.Parse(r.Selector)
		if err != nil {
			return false
		}
		current := ""
		if u.Scheme == "mesh" && u.Host != "" {
			current = u.Host
		}
		if u.Scheme == "cloud" {
			parts := strings.Split(u.Path, "/")
			if len(parts) == 5 && parts[1] == "tenant" {
				current = parts[2]
			}
		}
		parsed, err := meshid.ParseTenant(current)
		if err != nil {
			return false
		}
		if tenant != "" && tenant != parsed.String() {
			return false
		}
		tenant = parsed.String()
	}
	return tenant != ""
}
