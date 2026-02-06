package gui

import (
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tailscale.com/tsnet"
)

var (
	scanCache      = make(map[string][]portInfo)
	scanCacheMutex sync.RWMutex
	scanState      = make(map[string]int)
	scanStateMutex sync.RWMutex
)

//go:embed ui/templates/*.html ui/static/*
var uiFS embed.FS

var (
	templates     *template.Template
	staticOnce    sync.Once
	staticHandler http.Handler
)

func init() {
	var err error
	templates, err = template.ParseFS(uiFS, "ui/templates/*.html")
	if err != nil {
		panic(err)
	}
}

type portInfo struct {
	Port  uint16
	Label string
}

// Common HTTP/web application ports - scan entire range 20-9999
var commonHTTPPorts = generatePortRange(20, 9999)

func generatePortRange(start, end int) []int {
	ports := make([]int, 0, end-start+1)
	for i := start; i <= end; i++ {
		ports = append(ports, i)
	}
	return ports
}

// y u not export Discovered Endpoints in the API Tailscale? :sad:
func scanHostPorts(ctx context.Context, host string, ports []int, dialer func(context.Context, string, string) (net.Conn, error)) []portInfo {
	type result struct {
		port int
		open bool
	}

	results := make(chan result, len(ports))
	var wg sync.WaitGroup

	// Limit concurrent scans per host
	sem := make(chan struct{}, 50)

	for _, port := range ports {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				results <- result{port: p, open: false}
				return
			case sem <- struct{}{}:
				defer func() { <-sem }()
			}

			// Create a timeout context for this port dial
			dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()

			addr := net.JoinHostPort(host, strconv.Itoa(p))
			conn, err := dialer(dialCtx, "tcp", addr)
			if err == nil {
				conn.Close()
				results <- result{port: p, open: true}
			} else {
				results <- result{port: p, open: false}
			}
		}(port)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var openPorts []portInfo
	for res := range results {
		if res.open {
			openPorts = append(openPorts, portInfo{
				Port:  uint16(res.port),
				Label: strconv.Itoa(res.port),
			})
		}
	}
	return openPorts
}

type dashboardData struct {
	BaseDomain string
	Machines   []machineView
}

type machineView struct {
	Name         string
	DNSName      string
	StatusClass  string
	StatusText   string
	IPs          string
	CachedPorts  []portInfo
	Scanned      bool
	DefaultHTTPS bool
	HasPorts     bool
	Scanning     bool
}

func renderTemplate(w http.ResponseWriter, name string, data interface{}) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return templates.ExecuteTemplate(w, name, data)
}

func getStaticHandler() http.Handler {
	staticOnce.Do(func() {
		sub, err := fs.Sub(uiFS, "ui/static")
		if err != nil {
			staticHandler = http.NotFoundHandler()
			return
		}
		staticHandler = http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
	})
	return staticHandler
}

func RegisterHandlers(mux *http.ServeMux, s *tsnet.Server, baseDomain string) {
	if strings.TrimSpace(baseDomain) == "" {
		log.Fatal("baseDomain is required")
	}
	mux.Handle("/static/", getStaticHandler())
	mux.Handle("/ui/", http.RedirectHandler("/", http.StatusTemporaryRedirect))
	mux.Handle("/api/scan", handleScanAPI(s, baseDomain))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			serveDashboard(w, r, s, baseDomain)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
}

func handleScanAPI(s *tsnet.Server, baseDomain string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Machine string `json:"machine"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		localClient, err := s.LocalClient()
		if err != nil {
			http.Error(w, "failed to get client", http.StatusInternalServerError)
			return
		}

		status, err := localClient.Status(ctx)
		if err != nil {
			http.Error(w, "failed to get status", http.StatusInternalServerError)
			return
		}

		// Find the machine
		var targetIP string
		for _, peer := range status.Peer {
			peerName := peer.HostName
			dns := peer.DNSName
			if strings.HasSuffix(dns, ".") {
				dns = strings.TrimSuffix(dns, ".")
			}
			dns = strings.TrimSuffix(dns, "."+baseDomain)

			if peerName == req.Machine || dns == req.Machine {
				if len(peer.TailscaleIPs) > 0 {
					targetIP = peer.TailscaleIPs[0].String()
				}
				break
			}
		}

		if targetIP == "" {
			http.Error(w, "machine not found", http.StatusNotFound)
			return
		}

		startScan(req.Machine)
		defer finishScan(req.Machine)

		// Scan just this machine using Tailscale dialer
		scanCtx, scanCancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer scanCancel()

		openPorts := scanHostPorts(scanCtx, targetIP, generatePortRange(20, 9999), s.Dial)
		openPorts = sortPorts(openPorts)

		// Cache results
		scanCacheMutex.Lock()
		scanCache[req.Machine] = openPorts
		scanCacheMutex.Unlock()

		// Return JSON
		w.Header().Set("Content-Type", "application/json")
		portNums := make([]int, len(openPorts))
		for i, p := range openPorts {
			portNums[i] = int(p.Port)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ports": portNums,
		})
	}
}

func serveDashboard(w http.ResponseWriter, r *http.Request, s *tsnet.Server, baseDomain string) {
	lc, err := s.LocalClient()
	if err != nil {
		log.Printf("dashboard: failed to get local client: %v", err)
		showLoadingPage(w, baseDomain)
		return
	}

	ctx := r.Context()
	status, err := lc.Status(ctx)
	if err != nil {
		log.Printf("dashboard: failed to get status: %v", err)
		showLoadingPage(w, baseDomain)
		return
	}

	// If no peers are found, tsnet is probably still connecting
	if len(status.Peer) == 0 {
		log.Printf("dashboard: no peers found yet, showing loading page")
		showLoadingPage(w, baseDomain)
		return
	}

	data := dashboardData{
		BaseDomain: baseDomain,
		Machines:   []machineView{},
	}

	for _, peer := range status.Peer {
		if len(peer.TailscaleIPs) == 0 {
			continue
		}

		machineName := peer.DNSName
		if machineName != "" {
			// Strip trailing dot and domain
			machineName = strings.TrimSuffix(machineName, ".")
			machineName = strings.TrimSuffix(machineName, "."+baseDomain)
		}
		if machineName == "" {
			machineName = peer.HostName
		}

		statusClass := "offline"
		statusText := "offline"
		if peer.Online {
			statusClass = "online"
			statusText = "online"
		}

		cachedPorts, scanned := findCachedPorts(peer.DNSName, peer.HostName, machineName)
		scanning := findScanState(peer.DNSName, peer.HostName, machineName)
		hasPorts := len(cachedPorts) > 0
		defaultHTTPS := false
		if hasPorts {
			if cachedPorts[0].Port == 443 || cachedPorts[0].Port == 8448 {
				defaultHTTPS = true
			}
		}

		data.Machines = append(data.Machines, machineView{
			Name:         machineName,
			DNSName:      peer.DNSName,
			StatusClass:  statusClass,
			StatusText:   statusText,
			IPs:          strings.Join(formatIPs(peer.TailscaleIPs), ", "),
			CachedPorts:  cachedPorts,
			Scanned:      scanned,
			DefaultHTTPS: defaultHTTPS,
			HasPorts:     hasPorts,
			Scanning:     scanning,
		})
	}

	sort.Slice(data.Machines, func(i, j int) bool {
		return strings.ToLower(data.Machines[i].Name) < strings.ToLower(data.Machines[j].Name)
	})

	if err := renderTemplate(w, "dashboard.html", data); err != nil {
		log.Printf("dashboard: failed to render template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}

func formatIPs(ips []netip.Addr) []string {
	result := make([]string, len(ips))
	for i, ip := range ips {
		result[i] = ip.String()
	}
	return result
}

func findCachedPorts(dnsName string, hostName string, machineName string) ([]portInfo, bool) {
	scanCacheMutex.RLock()
	defer scanCacheMutex.RUnlock()

	if services, ok := scanCache[dnsName]; ok {
		return sortPorts(services), true
	}
	if services, ok := scanCache[hostName]; ok {
		return sortPorts(services), true
	}
	if services, ok := scanCache[machineName]; ok {
		return sortPorts(services), true
	}

	return nil, false
}

func startScan(machine string) {
	scanStateMutex.Lock()
	scanState[machine] = scanState[machine] + 1
	scanStateMutex.Unlock()
}

func finishScan(machine string) {
	scanStateMutex.Lock()
	if count, ok := scanState[machine]; ok {
		if count <= 1 {
			delete(scanState, machine)
		} else {
			scanState[machine] = count - 1
		}
	}
	scanStateMutex.Unlock()
}

func findScanState(dnsName string, hostName string, machineName string) bool {
	scanStateMutex.RLock()
	defer scanStateMutex.RUnlock()

	if count, ok := scanState[dnsName]; ok && count > 0 {
		return true
	}
	if count, ok := scanState[hostName]; ok && count > 0 {
		return true
	}
	if count, ok := scanState[machineName]; ok && count > 0 {
		return true
	}

	return false
}

func sortPorts(ports []portInfo) []portInfo {
	if len(ports) == 0 {
		return nil
	}

	sorted := make([]portInfo, len(ports))
	copy(sorted, ports)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Port < sorted[j].Port
	})
	return sorted
}

func showLoadingPage(w http.ResponseWriter, baseDomain string) {
	data := struct {
		BaseDomain string
	}{
		BaseDomain: baseDomain,
	}

	if err := renderTemplate(w, "loading.html", data); err != nil {
		log.Printf("dashboard: failed to render loading template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}
