package main

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"net/http/httputil"
	"os"
	"strconv"
	"strings"
	"time"

	"tailscale.com/tsnet"

	"github.com/jcambass/tailhopper/gui"
)

// TailHopper: A local proxy for personal Tailnet users.
// This program listens on localhost and proxies requests to machines in your Tailnet based on the URL path.
// Example usage:
//  1. Set TS_BASE_DOMAIN to your Tailnet's base domain (e.g. my.ts.net).
//  2. Optionally set TS_HOSTNAME to customize the Tailscale hostname (defaults to "tailhopper").
//  3. Optionally set LISTEN_PORT to change the listening port (defaults to 8888).
//  4. Run this program.
//  5. On first start, view stdout for a URL to authenticate with your Tailnet.
//  6. Access machines in your Tailnet via http://localhost:8888/proxy/{machine}/{port}/, e.g. http://localhost:8888/proxy/laptop/80/.
//     For HTTPS, use http://localhost:8888/proxy/https/{machine}/{port}/, e.g. http://localhost:8888/proxy/https/laptop/443/.
func main() {
	baseDomain := os.Getenv("TS_BASE_DOMAIN") // e.g. my.ts.net
	if baseDomain == "" {
		log.Fatal("set TS_BASE_DOMAIN, e.g. my.ts.net")
	}

	hostname := os.Getenv("TS_HOSTNAME")
	if hostname == "" {
		hostname = "tailhopper"
	}

	s := &tsnet.Server{
		Dir:      "./tsnet-state",
		Hostname: hostname,
	}
	defer s.Close()

	// Start the Tailscale connection early to show auth URL on startup
	if _, err := s.Up(context.Background()); err != nil {
		log.Fatal(err)
	}

	client := s.HTTPClient()
	rp := &httputil.ReverseProxy{Transport: client.Transport}
	if t, ok := rp.Transport.(*http.Transport); ok {
		if t.TLSClientConfig == nil {
			t.TLSClientConfig = &tls.Config{}
		}
		t.TLSClientConfig.InsecureSkipVerify = true
	}

	// Preserve the upstream Host header (important for many webapps + auth).
	rp.Director = func(r *http.Request) {
		machine, _ := r.Context().Value(machineKey{}).(string)
		port, _ := r.Context().Value(portKey{}).(string)
		useHTTPS, _ := r.Context().Value(httpsKey{}).(bool)

		scheme := "http"
		if useHTTPS {
			scheme = "https"
		}

		targetHost := machine + "." + baseDomain + ":" + port

		origHost := r.Host
		r.URL.Scheme = scheme
		r.URL.Host = targetHost
		r.Host = targetHost

		// Inform upstream it was accessed via a proxy.
		r.Header.Set("X-Forwarded-Proto", "http") // local side is http.
		r.Header.Set("X-Forwarded-Host", origHost)
	}

	// Helpful error logging
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("proxy error: %v", err)
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}

	mux := http.NewServeMux()
	mux.Handle("/proxy/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		useHTTPS := false
		if strings.HasPrefix(r.URL.Path, "/proxy/https/") {
			useHTTPS = true
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/proxy/https")
		} else {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/proxy")
		}

		machine, port, rest, ok := parseMachinePortPath(r.URL.Path)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if !isValidMachineName(machine) {
			http.Error(w, "invalid machine name", http.StatusBadRequest)
			return
		}
		if !isValidPort(port) {
			http.Error(w, "invalid port", http.StatusBadRequest)
			return
		}
		if rest == "/" && !strings.HasSuffix(r.URL.Path, "/") {
			redirectPath := r.URL.Path + "/"
			if r.URL.RawQuery != "" {
				redirectPath += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, redirectPath, http.StatusTemporaryRedirect)
			return
		}

		r.URL.Path = rest
		r = r.WithContext(context.WithValue(r.Context(), machineKey{}, machine))
		r = r.WithContext(context.WithValue(r.Context(), portKey{}, port))
		r = r.WithContext(context.WithValue(r.Context(), httpsKey{}, useHTTPS))
		rp.ServeHTTP(w, r)
	}))

	// Optional GUI
	gui.RegisterHandlers(mux, s, baseDomain)

	listenPort := os.Getenv("LISTEN_PORT")
	if listenPort == "" {
		listenPort = "8888"
	}
	localAddr := "127.0.0.1:" + listenPort
	log.Printf("listening on http://%s -> *. %s", localAddr, baseDomain)

	// NOTE: Avoid setting aggressive Read/Write timeouts here; they can break long-lived websockets.
	srv := &http.Server{
		Addr:              localAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

type machineKey struct{}
type portKey struct{}
type httpsKey struct{}

func parseMachinePortPath(path string) (machine string, port string, rest string, ok bool) {
	trimmed := strings.TrimPrefix(path, "/")
	if trimmed == "" {
		return "", "", "", false
	}

	parts := strings.SplitN(trimmed, "/", 3)
	machine = parts[0]
	if machine == "" {
		return "", "", "", false
	}
	if len(parts) < 2 || parts[1] == "" {
		return "", "", "", false
	}
	port = parts[1]

	rest = "/"
	if len(parts) == 3 && parts[2] != "" {
		rest = "/" + parts[2]
	}

	return machine, port, rest, true
}

func isValidMachineName(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			continue
		}
		return false
	}
	return name != ""
}

func isValidPort(port string) bool {
	value, err := strconv.Atoi(port)
	if err != nil {
		return false
	}
	return value >= 1 && value <= 65535
}
