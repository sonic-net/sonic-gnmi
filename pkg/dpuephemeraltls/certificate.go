package dpuephemeraltls

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"sort"
	"time"
)

const certificateLifetime = 10 * 365 * 24 * time.Hour

var interfaceAddrs = net.InterfaceAddrs
var hostName = os.Hostname

// New generates a self-signed server certificate for the DPU's current identities.
func New(now time.Time) (tls.Certificate, error) {
	addrs, err := interfaceAddrs()
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("list interface addresses: %w", err)
	}

	ipAddresses := make([]net.IP, 0, len(addrs))
	seen := map[string]bool{}
	for _, addr := range addrs {
		var ip net.IP
		switch addr := addr.(type) {
		case *net.IPNet:
			ip = addr.IP
		case *net.IPAddr:
			ip = addr.IP
		default:
			ip, _, _ = net.ParseCIDR(addr.String())
		}
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			continue
		}
		key := ip.String()
		if !seen[key] {
			seen[key] = true
			ipAddresses = append(ipAddresses, ip)
		}
	}
	if len(ipAddresses) == 0 {
		return tls.Certificate{}, fmt.Errorf("no non-loopback IP addresses found")
	}
	sort.Slice(ipAddresses, func(i, j int) bool {
		return ipAddresses[i].String() < ipAddresses[j].String()
	})

	dnsNames := []string{}
	hostname, _ := hostName()
	if hostname != "" {
		dnsNames = append(dnsNames, hostname)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate serial number: %w", err)
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate private key: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{Organization: []string{"SONiC DPU"}},
		DNSNames:     dnsNames,
		IPAddresses:  ipAddresses,
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.Add(certificateLifetime),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	derBytes, err := x509.CreateCertificate(
		rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load generated key pair: %w", err)
	}
	certificate.Leaf, err = x509.ParseCertificate(derBytes)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse generated certificate: %w", err)
	}
	return certificate, nil
}
