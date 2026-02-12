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

func formatIPs(ips []netip.Prefix) []string {
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

// RenderToast renders a toast notification as HTML string.
func RenderToast(toastType, message string) (string, error) {
	var buf strings.Builder
	data := struct {
		Type    string
		Message string
	}{
		Type:    toastType,
		Message: message,
	}
	if err := templates.ExecuteTemplate(&buf, "toast", data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// dashboardData contains all data needed to render the dashboard.
type dashboardData struct {
	PACFileURL              string
	Tailnets                []tailnetCard
	HasUnconfiguredTailnets bool
}

// tailnetCard contains all data for rendering a single tailnet card.
type tailnetCard struct {
	ID             string
	BaseDomain     string
	Hostname       string
	SocksAddr      string
	SocksHost      string
	SocksPort      string
	Machines       []machineView
	StateClass     string // "connected", "needs-login", "connecting", "error"
	AuthURL        string
	LifecycleState string
	ErrorMsg       string
}

// machineView represents a machine for display.
type machineView struct {
	Name        string
	DNSName     string
	StatusClass string
	StatusText  string
	IPs         string
}
