// Package ui provides HTTP handlers and templates for the Tailhopper dashboard.
package ui

import (
	"embed"
	"html/template"
	"net/http"
	"net/netip"
	"strings"
	"sync"
)

//go:embed templates/*.html templates/partials/*.html templates/partials/*.svg static/*
var uiFS embed.FS

var (
	templates     *template.Template
	staticOnce    sync.Once
	staticHandler http.Handler
)

func init() {
	var err error
	templates, err = template.New("").ParseFS(uiFS, "templates/*.html", "templates/partials/*.html", "templates/partials/*.svg")
	if err != nil {
		panic(err)
	}
}

func formatIPs(ips []netip.Addr) []string {
	result := make([]string, len(ips))
	for i, ip := range ips {
		result[i] = ip.String()
	}
	return result
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

// deriveMachineName extracts the short machine name from DNSName or falls back to HostName.
func deriveMachineName(dnsName, hostName, baseDomain string) string {
	name := dnsName
	if name != "" {
		name = strings.TrimSuffix(name, ".")
		name = strings.TrimSuffix(name, "."+baseDomain)
	}
	if name == "" {
		name = hostName
	}
	return name
}

// dashboardData contains all data needed to render the dashboard.
type dashboardData struct {
	BaseDomain string
	Hostname   string
	SocksAddr  string
	SocksHost  string
	SocksPort  string
	PACFileURL string
	Machines   []machineView
	State      string // StateConnected, StateConnecting, etc.
	StateClass string // "connected", "needs-login", "connecting"
	AuthURL    string
}

// machineView represents a machine for display.
type machineView struct {
	Name        string
	DNSName     string
	StatusClass string
	StatusText  string
	IPs         string
}
