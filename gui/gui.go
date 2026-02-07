// Package gui provides HTTP handlers and templates for the Tailhopper dashboard.
package gui

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

//go:embed ui/templates/*.html ui/templates/partials/*.html ui/static/*
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
	templates, err = template.New("").Funcs(funcMap).ParseFS(uiFS, "ui/templates/*.html", "ui/templates/partials/*.html")
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
	BaseDomain  string
	Hostname    string
	SocksAddr   string
	SocksHost   string
	SocksPort   string
	PACFileURL  string
	Machines    []machineView
	Connections []connectionView
}

// connectionView represents a connection for display.
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

// machineView represents a machine for display.
type machineView struct {
	Name         string
	DNSName      string
	StatusClass  string
	StatusText   string
	IPs          string
	CachedPorts  []int
	Scanned      bool
	DefaultHTTPS bool
	HasPorts     bool
	Scanning     bool
}
