package download

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestDownloadTrustedTLS(t *testing.T) {
	testCase := os.Getenv("GNOI_TRUSTED_TLS_TEST")
	if testCase == "" {
		for _, name := range []string{"trusted hostname", "untrusted CA", "hostname mismatch"} {
			t.Run(name, func(t *testing.T) {
				// Each process gets a fresh system root pool, without changing
				// production TLS configuration or relying on the machine's CAs.
				cmd := exec.Command(os.Args[0], "-test.run=^TestDownloadTrustedTLS$")
				cmd.Env = append(os.Environ(), "GNOI_TRUSTED_TLS_TEST="+name)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v: %s", err, output)
				}
			})
		}
		return
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("TLS image bytes"))
	}))
	defer server.Close()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "roots.pem")
	var roots []byte
	if testCase != "untrusted CA" {
		roots = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	}
	if err := os.WriteFile(certPath, roots, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", certPath)
	t.Setenv("SSL_CERT_DIR", dir)
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid:1")
	// httptest's certificate is valid for example.com, not this pinned IP's
	// invented hostname. A hostname mismatch must still fail with a trusted CA.
	host := "example.com"
	if testCase == "hostname mismatch" {
		host = "mismatch.example"
	}
	_, portText, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	policy, _ := json.Marshal(map[string]interface{}{
		"origins": []map[string]interface{}{{
			"scheme": "https", "host": host, "port": port, "addresses": []string{"127.0.0.1"},
		}},
	})
	policyPath := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policyPath, policy, 0600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "image")
	err := DownloadTrusted(context.Background(), "https://"+host+":"+portText+"/image", policyPath, dst, 100)
	data, readErr := os.ReadFile(dst)
	if testCase == "trusted hostname" {
		if err != nil || readErr != nil || string(data) != "TLS image bytes" {
			t.Fatalf("download=%v, read=%v, payload=%q", err, readErr, data)
		}
	} else if err == nil || !os.IsNotExist(readErr) {
		t.Fatalf("TLS failure must reject without destination: download=%v, read=%v", err, readErr)
	}
}

func TestDownloadTrusted(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/image", http.StatusFound)
			return
		}

		_, _ = w.Write([]byte("image bytes"))
	}))
	defer server.Close()
	_, portText, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	// This deliberately unresolvable name must use only the administrator's pin.
	origin := "http://images.example:" + portText
	policy, _ := json.Marshal(map[string]interface{}{
		"origins": []map[string]interface{}{{
			"scheme": "http", "host": "images.example", "port": port,
			"addresses": []string{"127.0.0.1"},
		}},
	})
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policyPath, policy, 0600); err != nil {
		t.Fatal(err)
	}
	// An ambient proxy must neither receive the request nor affect its destination.
	t.Setenv("HTTP_PROXY", "http://proxy.invalid:1")
	for _, tt := range []struct {
		name, url, policy string
		limit             int64
		wantErr           bool
		wantRequests      int
	}{
		{"allowed pinned host", origin + "/image", string(policy), 100, false, 1},
		{"untrusted host", "http://other.example:" + portText, string(policy), 100, true, 0},
		{"untrusted port", "http://images.example:1/image", string(policy), 100, true, 0},
		{"untrusted scheme", "https://images.example:" + portText, string(policy), 100, true, 0},
		{"userinfo", "http://user:pass@images.example:" + portText, string(policy), 100, true, 0},
		{"malformed policy", origin, "{", 100, true, 0},
		{"unknown policy field", origin, `{"origins":[],"allow_all":true}`, 100, true, 0},
		{"empty policy", origin, `{"origins":[]}`, 100, true, 0},
		{"redirect rejected", origin + "/redirect", string(policy), 100, true, 1},
		{"size limit", origin + "/image", string(policy), 3, true, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests = 0
			if err := os.WriteFile(policyPath, []byte(tt.policy), 0600); err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(t.TempDir(), "image")
			err := DownloadTrusted(context.Background(), tt.url, policyPath, dst, tt.limit)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if requests != tt.wantRequests {
				t.Fatalf("upstream requests = %d, want %d", requests, tt.wantRequests)
			}
			data, readErr := os.ReadFile(dst)
			if !tt.wantErr && (readErr != nil || string(data) != "image bytes") {
				t.Fatalf("payload = %q, read error = %v", data, readErr)
			}
			if tt.wantErr && !os.IsNotExist(readErr) {
				t.Fatalf("failed download left destination: %v", readErr)
			}
		})
	}
	if err := DownloadTrusted(context.Background(), origin, filepath.Join(dir, "absent"), filepath.Join(dir, "unused"), 100); err == nil {
		t.Fatal("missing policy accepted")
	}
}

func TestTrustedAddressPolicy(t *testing.T) {
	for _, tt := range []struct {
		name, policy, url string
		wantErr           bool
	}{
		{"ipv6 literal", `{"origins":[{"scheme":"https","host":"2001:db8::1","port":443,"addresses":["2001:db8::1"]}]}`,
			"https://[2001:db8::1]/image", false},
		{"ipv4 literal", `{"origins":[{"scheme":"http","host":"192.0.2.1","port":80,"addresses":["192.0.2.1"]}]}`,
			"http://192.0.2.1/image", false},
		{"missing pins", `{"origins":[{"scheme":"http","host":"images.example","port":80}]}`,
			"http://images.example/image", true},
		{"literal mismatch", `{"origins":[{"scheme":"http","host":"192.0.2.1","port":80,"addresses":["192.0.2.2"]}]}`,
			"http://192.0.2.1/image", true},
		{"scoped ipv6", `{"origins":[{"scheme":"http","host":"images.example","port":80,"addresses":["fe80::1%eth0"]}]}`,
			"http://images.example/image", true},
		{"hostname pin", `{"origins":[{"scheme":"https","host":"images.example","port":443,"addresses":["2001:db8::1","192.0.2.1"]}]}`,
			"https://images.example/image", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "policy")
			if err := os.WriteFile(path, []byte(tt.policy), 0600); err != nil {
				t.Fatal(err)
			}
			origins, err := loadTrustedOrigins(path)
			if err == nil {
				_, _, err = matchTrustedOrigin(tt.url, origins)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
