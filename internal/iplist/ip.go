package iplist

import (
	"fmt"
	"net"
)

func FilterIPv4Only(ips []net.IP) []net.IP {
	var filtered []net.IP
	for _, ip := range ips {
		if ip.To4() != nil {
			filtered = append(filtered, ip)
		}
	}
	return filtered
}

func FilterIPv6Only(ips []net.IP) []net.IP {
	var filtered []net.IP
	for _, ip := range ips {
		if ip.To4() == nil {
			filtered = append(filtered, ip)
		}
	}
	return filtered
}

type InvalidIPError struct {
	ip string
}

func (e InvalidIPError) Error() string {
	return fmt.Sprintf("failed to parse IP '%s'", e.ip)
}

func ParseIPs(ips []string) ([]net.IP, error) {
	var parsed []net.IP
	for _, ipString := range ips {
		ip := net.ParseIP(ipString)
		if ip == nil {
			return nil, InvalidIPError{ip: ipString}
		}
		parsed = append(parsed, ip)
	}

	return parsed, nil
}
