package download

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

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
