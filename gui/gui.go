package gui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jcambass/tailhopper/pac"
	"github.com/jcambass/tailhopper/portscan"
	"github.com/jcambass/tailhopper/socks"
	"github.com/jcambass/tailhopper/ts"
)

var (
	// Scan cache for port scanning results
	scanCache = portscan.NewCache()

	// Channel for tsnet connection error (buffered so send doesn't block)
	TsnetErrorCh = make(chan error, 1)
	// Channel that gets closed when tsnet is ready
	TsnetReadyCh = make(chan struct{})
	// Channel that gets closed when connection is taking too long
	TsnetSlowCh = make(chan struct{})
	// Channel for auth URL when login is needed (buffered so send doesn't block)
	TsnetAuthURLCh = make(chan string, 1)
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
	funcMap := template.FuncMap{
		"formatDuration": formatDuration,
		"formatBytes":    formatBytes,
	}
	templates, err = template.New("").Funcs(funcMap).ParseFS(uiFS, "ui/templates/*.html")
	if err != nil {
		panic(err)
	}
}

func formatDuration(start, end time.Time) string {
	if end.IsZero() {
		return "ongoing"
	}
	d := end.Sub(start)
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fm", d.Minutes())
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}

type dashboardData struct {
	BaseDomain  string
	Hostname    string
	SocksAddr   string
	SocksHost   string
	SocksPort   string
	PACFileURL  string
	Machines    []machineView
	Connections []connectionView
}

type connectionView struct {
	Host      string
	Port      string
	StartTime time.Time
	EndTime   time.Time
	BytesSent int64
	BytesRecv int64
	Active    bool
	Error     string
}

type machineView struct {
	Name         string
	DNSName      string
	StatusClass  string
	StatusText   string
	IPs          string
	CachedPorts  []portscan.PortInfo
	Scanned      bool
	DefaultHTTPS bool
	HasPorts     bool
	Scanning     bool
}

func renderTemplate(w http.ResponseWriter, name string, data interface{}) error {
	var buf strings.Builder
	if err := templates.ExecuteTemplate(&buf, name, data); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := w.Write([]byte(buf.String()))
	return err
}

// StaticHandler returns an http.Handler for serving static files.
func StaticHandler() http.Handler {
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

// HandleConnectionsAPI returns a handler for the connections API endpoint.
func HandleConnectionsAPI(connLog *socks.ConnectionLog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		recent, live := connLog.GetRecent(20)

		connections := make([]connectionView, 0, len(live)+len(recent))
		for _, lc := range live {
			connections = append(connections, connectionView{
				Host:      lc.Host,
				Port:      lc.Port,
				StartTime: lc.StartTime,
				BytesSent: lc.BytesSent,
				BytesRecv: lc.BytesRecv,
				Active:    true,
			})
		}
		for _, c := range recent {
			connections = append(connections, connectionView{
				Host:      c.Host,
				Port:      c.Port,
				StartTime: c.StartTime,
				EndTime:   c.EndTime,
				BytesSent: c.BytesSent,
				BytesRecv: c.BytesRecv,
				Error:     c.Error,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(connections)
	}
}

// HandleMachinesAPI returns a handler for the machines API endpoint.
func HandleMachinesAPI(tsServer *ts.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		baseDomain := tsServer.BaseDomain()

		lc, err := tsServer.LocalClient()
		if err != nil {
			http.Error(w, "failed to get local client", http.StatusInternalServerError)
			return
		}

		status, err := lc.Status(r.Context())
		if err != nil {
			http.Error(w, "failed to get status", http.StatusInternalServerError)
			return
		}

		machines := []machineView{}

		for _, peer := range status.Peer {
			if len(peer.TailscaleIPs) == 0 {
				continue
			}

			machineName := peer.DNSName
			if machineName != "" {
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

			machines = append(machines, machineView{
				Name:        machineName,
				DNSName:     peer.DNSName,
				StatusClass: statusClass,
				StatusText:  statusText,
				IPs:         strings.Join(formatIPs(peer.TailscaleIPs), ", "),
			})
		}

		sort.Slice(machines, func(i, j int) bool {
			return strings.ToLower(machines[i].Name) < strings.ToLower(machines[j].Name)
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(machines)
	}
}

// HandleScanAPI returns a handler for the port scanning API endpoint.
func HandleScanAPI(tsServer *ts.Server) http.HandlerFunc {
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

		baseDomain := tsServer.BaseDomain()

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		localClient, err := tsServer.LocalClient()
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
			dns := strings.TrimSuffix(peer.DNSName, ".")
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

		scanCache.StartScan(req.Machine)
		defer scanCache.FinishScan(req.Machine)

		// Scan just this machine using Tailscale dialer
		scanCtx, scanCancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer scanCancel()

		openPorts := portscan.ScanHost(scanCtx, targetIP, portscan.CommonHTTPPorts(), tsServer.Dial)
		openPorts = portscan.SortPorts(openPorts)

		// Cache results
		scanCache.Set(req.Machine, openPorts)

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

// ServeDashboard renders the main dashboard page.
func ServeDashboard(w http.ResponseWriter, r *http.Request, tsServer *ts.Server, socksAddr string, connLog *socks.ConnectionLog) {
	// Try to get baseDomain early - may be available from cached state even before fully connected
	baseDomain := tsServer.BaseDomain()

	// Check for tsnet connection error first (non-blocking)
	select {
	case err := <-TsnetErrorCh:
		log.Printf("dashboard: got error from TsnetErrorCh: %v", err)
		// Put it back so subsequent calls also see it
		TsnetErrorCh <- err
		showErrorPage(w, err.Error())
		return
	default:
	}

	// Check if login is needed (non-blocking)
	select {
	case authURL := <-TsnetAuthURLCh:
		// Put it back so subsequent calls also see it
		TsnetAuthURLCh <- authURL
		showLoginPage(w, authURL)
		return
	default:
	}

	// Check if tsnet is ready (non-blocking)
	select {
	case <-TsnetReadyCh:
		// Ready, continue
	default:
		// Not ready yet - check if it's taking too long
		select {
		case <-TsnetSlowCh:
			// Taking too long, show slow connection page
			showSlowConnectionPage(w, baseDomain)
		default:
			// Still within normal time, show loading page
			showLoadingPage(w, baseDomain)
		}
		return
	}

	lc, err := tsServer.LocalClient()
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

	// Parse host and port from socksAddr
	socksHost, socksPort, _ := net.SplitHostPort(socksAddr)

	// Get connections
	recent, live := connLog.GetRecent(20)
	connections := make([]connectionView, 0, len(live)+len(recent))
	for _, c := range live {
		connections = append(connections, connectionView{
			Host:      c.Host,
			Port:      c.Port,
			StartTime: c.StartTime,
			BytesSent: c.BytesSent,
			BytesRecv: c.BytesRecv,
			Active:    true,
		})
	}
	for _, c := range recent {
		connections = append(connections, connectionView{
			Host:      c.Host,
			Port:      c.Port,
			StartTime: c.StartTime,
			EndTime:   c.EndTime,
			BytesSent: c.BytesSent,
			BytesRecv: c.BytesRecv,
			Error:     c.Error,
		})
	}

	// Get our hostname from status
	hostname := ""
	if status.Self != nil {
		hostname = status.Self.HostName
	}

	data := dashboardData{
		BaseDomain:  baseDomain,
		Hostname:    hostname,
		SocksAddr:   socksAddr,
		SocksHost:   socksHost,
		SocksPort:   socksPort,
		PACFileURL:  pac.URLPath,
		Machines:    []machineView{},
		Connections: connections,
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

func findCachedPorts(dnsName string, hostName string, machineName string) ([]portscan.PortInfo, bool) {
	return scanCache.Get(dnsName, hostName, machineName)
}

func findScanState(dnsName string, hostName string, machineName string) bool {
	return scanCache.IsScanning(dnsName, hostName, machineName)
}

func showLoadingPage(w http.ResponseWriter, baseDomain string) {
	if baseDomain == "" {
		baseDomain = "Tailscale"
	}
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

func showErrorPage(w http.ResponseWriter, errMsg string) {
	data := struct {
		Error string
	}{
		Error: errMsg,
	}

	if err := renderTemplate(w, "error.html", data); err != nil {
		log.Printf("dashboard: failed to render error template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}

func showSlowConnectionPage(w http.ResponseWriter, baseDomain string) {
	if baseDomain == "" {
		baseDomain = "Tailscale"
	}
	data := struct {
		BaseDomain string
	}{
		BaseDomain: baseDomain,
	}

	if err := renderTemplate(w, "slow.html", data); err != nil {
		log.Printf("dashboard: failed to render slow template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}

func showLoginPage(w http.ResponseWriter, authURL string) {
	data := struct {
		AuthURL string
	}{
		AuthURL: authURL,
	}

	if err := renderTemplate(w, "login.html", data); err != nil {
		log.Printf("dashboard: failed to render login template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}
