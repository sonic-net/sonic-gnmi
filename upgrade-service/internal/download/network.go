package download

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/golang/glog"
)

const (
	// DefaultInterface is the default management interface to check.
	DefaultInterface = "eth0"
	// DefaultInterfaceStatePath is the path to check interface operational state.
	DefaultInterfaceStatePath = "/sys/class/net/eth0/operstate"
)

// InterfaceInfo contains information about a network interface.
type InterfaceInfo struct {
	Name      string
	IsUp      bool
	IPv4Addrs []string
	IPv6Addrs []string
}

// IsInterfaceUp checks if the specified network interface is operationally up.
func IsInterfaceUp(interfaceName string) bool {
	statePath := fmt.Sprintf("/sys/class/net/%s/operstate", interfaceName)
	data, err := os.ReadFile(statePath)
	if err != nil {
		glog.V(2).Infof("Failed to read interface state for %s: %v", interfaceName, err)
		return false
	}

	state := strings.TrimSpace(string(data))
	isUp := state == "up"
	glog.V(2).Infof("Interface %s state: %s (up: %t)", interfaceName, state, isUp)
	return isUp
}

// GetInterfaceInfo retrieves detailed information about a network interface.
func GetInterfaceInfo(interfaceName string) (*InterfaceInfo, error) {
	info := &InterfaceInfo{
		Name:      interfaceName,
		IPv4Addrs: make([]string, 0),
		IPv6Addrs: make([]string, 0),
	}

	// Check if interface is up
	info.IsUp = IsInterfaceUp(interfaceName)

	// Get interface details
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return info, fmt.Errorf("interface %s not found: %w", interfaceName, err)
	}

	// Get IP addresses
	addrs, err := iface.Addrs()
	if err != nil {
		return info, fmt.Errorf("failed to get addresses for interface %s: %w", interfaceName, err)
	}

	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}

		// Skip loopback addresses
		if ipNet.IP.IsLoopback() {
			continue
		}

		// Categorize by IP version
		if ipNet.IP.To4() != nil {
			// IPv4 address
			info.IPv4Addrs = append(info.IPv4Addrs, ipNet.IP.String())
			glog.V(2).Infof("Found IPv4 address %s on interface %s", ipNet.IP.String(), interfaceName)
		} else if !ipNet.IP.IsLinkLocalUnicast() {
			// IPv6 address - skip link-local addresses
			info.IPv6Addrs = append(info.IPv6Addrs, ipNet.IP.String())
			glog.V(2).Infof("Found IPv6 address %s on interface %s", ipNet.IP.String(), interfaceName)
		}
	}

	return info, nil
}

// ExtractHostFromURL extracts the host portion from a URL.
func ExtractHostFromURL(rawURL string) (string, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse URL: %w", err)
	}

	host := parsedURL.Hostname()
	if host == "" {
		return "", fmt.Errorf("no host found in URL")
	}

	return host, nil
}

// DetectIPVersion determines if a host string is IPv4, IPv6, or hostname.
func DetectIPVersion(host string) string {
	// Remove brackets if present (IPv6 URLs often have brackets)
	host = strings.Trim(host, "[]")

	// Try to parse as IP
	ip := net.ParseIP(host)
	if ip == nil {
		return "hostname"
	}

	if ip.To4() != nil {
		return "ipv4"
	}
	return "ipv6"
}

// IsIPv4Address checks if a string is a valid IPv4 address using regex.
// This mirrors the bash regex: ^([0-9]{1,3}\.){3}[0-9]{1,3}$.
func IsIPv4Address(host string) bool {
	ipv4Regex := regexp.MustCompile(`^([0-9]{1,3}\.){3}[0-9]{1,3}$`)
	return ipv4Regex.MatchString(host)
}

// IsIPv6Address checks if a string is a valid IPv6 address using regex.
// This mirrors the bash regex for bracketed IPv6: ^\[([0-9a-fA-F]{0,4}:){1,7}[0-9a-fA-F]{0,4}\]$.
func IsIPv6Address(host string) bool {
	// Check both bracketed and unbracketed forms
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		// Bracketed form
		ipv6Regex := regexp.MustCompile(`^\[([0-9a-fA-F]{0,4}:){1,7}[0-9a-fA-F]{0,4}\]$`)
		return ipv6Regex.MatchString(host)
	}

	// Try parsing as IP directly
	ip := net.ParseIP(host)
	return ip != nil && ip.To4() == nil
}

// GetRelevantIPAddresses returns IP addresses from the interface that match the target URL's IP version.
func GetRelevantIPAddresses(info *InterfaceInfo, targetURL string) []string {
	host, err := ExtractHostFromURL(targetURL)
	if err != nil {
		glog.V(2).Infof("Failed to extract host from URL %s: %v", targetURL, err)
		return nil
	}

	ipVersion := DetectIPVersion(host)
	glog.V(2).Infof("Target URL %s has host %s with IP version: %s", targetURL, host, ipVersion)

	switch ipVersion {
	case "ipv4":
		return info.IPv4Addrs
	case "ipv6":
		return info.IPv6Addrs
	default:
		// For hostnames, try both IPv4 and IPv6, preferring IPv4
		result := make([]string, 0, len(info.IPv4Addrs)+len(info.IPv6Addrs))
		result = append(result, info.IPv4Addrs...)
		result = append(result, info.IPv6Addrs...)
		return result
	}
}
