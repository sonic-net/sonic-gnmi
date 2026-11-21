package download

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// TrustedPolicyPath is read on each SetPackage download, from the host's
// read-only /etc/sonic mount when running in the gNMI container.
const TrustedPolicyPath = "/etc/sonic/gnoi-download.json"

type trustedOrigin struct {
	Scheme    string   `json:"scheme"`
	Host      string   `json:"host"`
	Port      int      `json:"port"`
	Addresses []string `json:"addresses"`
}

func loadTrustedOrigins(path string) ([]trustedOrigin, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("trusted download policy unavailable: %w", err)
	}
	defer f.Close()
	var policy struct {
		Origins []trustedOrigin `json:"origins"`
	}
	data, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return nil, errors.New("trusted download policy unreadable or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return nil, fmt.Errorf("invalid trusted download policy: %w", err)
	}
	if decoder.Decode(new(interface{})) != io.EOF || len(policy.Origins) == 0 {
		return nil, errors.New("invalid or empty trusted download policy")
	}
	for _, origin := range policy.Origins {
		if (origin.Scheme != "http" && origin.Scheme != "https") ||
			origin.Port < 1 || origin.Port > 65535 ||
			origin.Host == "" || strings.ContainsAny(origin.Host, "/@%?#[] \t\r\n") {
			return nil, errors.New("invalid trusted origin")
		}
		if ip, err := netip.ParseAddr(origin.Host); err == nil {
			if ip.Zone() != "" {
				return nil, errors.New("scoped addresses are not supported")
			}
		} else if strings.Contains(origin.Host, ":") {
			return nil, errors.New("invalid trusted host")
		}
		if len(origin.Addresses) == 0 {
			return nil, errors.New("trusted origin requires pinned addresses")
		}
		for _, address := range origin.Addresses {
			ip, err := netip.ParseAddr(address)
			if err != nil || ip.Zone() != "" {
				return nil, errors.New("trusted origin requires unscoped IP addresses")
			}
			// A literal URL cannot use a different address supplied by policy.
			if hostIP, err := netip.ParseAddr(origin.Host); err == nil && hostIP.Unmap() != ip.Unmap() {
				return nil, errors.New("literal origin address does not match host")
			}
		}
	}
	return policy.Origins, nil
}

func matchTrustedOrigin(rawURL string, origins []trustedOrigin) (*url.URL, *trustedOrigin, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" ||
		u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") ||
		strings.Contains(u.Hostname(), "%") {
		return nil, nil, errors.New("invalid download URL")
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	for i := range origins {
		origin := &origins[i]
		if u.Scheme == origin.Scheme && strings.EqualFold(u.Hostname(), origin.Host) &&
			port == strconv.Itoa(origin.Port) {
			return u, origin, nil
		}
	}
	return nil, nil, errors.New("download origin is not trusted")
}

// DownloadTrusted fetches only an administrator-configured origin. Hostnames
// use explicit IP pins instead of DNS, so resolution cannot change the approved
// connection destination. Redirects and ambient proxies are never followed.
// The caller must supply a new destination inside a private staging directory.
func DownloadTrusted(ctx context.Context, rawURL, policyPath, dst string, maxSize int64) error {
	origins, err := loadTrustedOrigins(policyPath)
	if err != nil {
		return err
	}
	u, origin, err := matchTrustedOrigin(rawURL, origins)
	if err != nil {
		return err
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	transport := &http.Transport{
		Proxy:              nil,
		DisableCompression: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" || address != net.JoinHostPort(u.Hostname(), strconv.Itoa(origin.Port)) {
				return nil, errors.New("untrusted connection destination")
			}
			var lastErr error
			for _, pin := range origin.Addresses {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(pin, strconv.Itoa(origin.Port)))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("download redirects are not supported")
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("trusted download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	if maxSize <= 0 || resp.ContentLength > maxSize {
		return errors.New("download exceeds size limit")
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, maxSize+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil || n == 0 || n > maxSize {
		_ = os.Remove(dst)
		return fmt.Errorf("incomplete or oversized download: bytes=%d, copy=%v, close=%v", n, copyErr, closeErr)
	}
	return nil
}
