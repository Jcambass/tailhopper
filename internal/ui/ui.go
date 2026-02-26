// Package ui provides HTTP handlers and templates for the Tailhopper dashboard.
package ui

import (
	"embed"
	"html/template"
	"net/http"
	"net/netip"
	"strings"
	"sync"

	"github.com/jcambass/tailhopper/internal/ts"
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
	HasTailnets             bool
}

type StateClass string

const (
	StateClassConnected  StateClass = "connected"
	StateClassNeedsLogin StateClass = "needs-login"
	StateClassNeedsAuth  StateClass = "needs-auth"
	StateClassConnecting StateClass = "connecting"
	StateClassDisabling  StateClass = "disabling"
	StateClassDisabled   StateClass = "disabled"
	StateClassError      StateClass = "error"
	StateClassLoggingOut StateClass = "logging-out"
)

// tailnetCard contains all data for rendering a single tailnet card.
type tailnetCard struct {
	ID         int
	BaseDomain string
	SocksAddr  string
	SocksHost  string
	SocksPort  string
	Machines   []machineView
	stateName  ts.State
	Hostname   string
	AuthURL    string
	ErrorMsg   string
}

func (c tailnetCard) StateClass() StateClass {
	switch c.stateName {
	case ts.ConnectedState:
		return StateClassConnected
	case ts.HasTerminalErrorState:
		return StateClassError
	case ts.NeedsLoginState:
		return StateClassNeedsLogin
	case ts.NeedsMachineAuthState:
		return StateClassNeedsAuth
	case ts.StartingState, ts.StartedState:
		return StateClassConnecting
	case ts.StoppingState:
		return StateClassDisabling
	case ts.StoppedState:
		return StateClassDisabled
	case ts.LoggingOutState:
		return StateClassLoggingOut
	default:
		panic("unexpected state name: " + string(c.stateName))
	}
}

func (c tailnetCard) IsToggleOn() bool {
	return c.stateName != ts.StoppingState && c.stateName != ts.StoppedState && c.stateName != ts.HasTerminalErrorState && c.stateName != ts.LoggingOutState
}

func (c tailnetCard) IsToggleDisabled() bool {
	return c.stateName == ts.StartingState || c.stateName == ts.StoppingState || c.stateName == ts.HasTerminalErrorState || c.stateName == ts.LoggingOutState
}

func (c tailnetCard) ToggleAction() string {
	if c.IsToggleOn() {
		return "stop"
	}
	return "start"
}

func (c tailnetCard) IsErrorState() bool {
	return c.stateName == ts.HasTerminalErrorState
}

// machineView represents a machine for display.
type machineView struct {
	Name        string
	DNSName     string
	StatusClass string
	StatusText  string
	IPs         string
}
