// Package ui provides HTTP handlers and templates for the Tailhopper dashboard.
package ui

import (
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"

	"github.com/jcambass/tailhopper/internal/pac"
	"github.com/jcambass/tailhopper/internal/registry"
	"tailscale.com/ipn"
)

//go:embed templates/*.html templates/partials/*.html templates/partials/*.svg static/*
var uiFS embed.FS

var (
	templates     *template.Template
	staticHandler http.Handler
)

func init() {
	var err error
	templates, err = template.New("").ParseFS(uiFS, "templates/*.html", "templates/partials/*.html", "templates/partials/*.svg")
	if err != nil {
		panic(err)
	}
	static, err := fs.Sub(uiFS, "static")
	if err != nil {
		panic(err)
	}
	staticHandler = http.StripPrefix("/static/", http.FileServer(http.FS(static)))
}

func StaticHandler() http.Handler {
	return staticHandler
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
	StateClassDisabled   StateClass = "disabled"
	StateClassError      StateClass = "error"
)

// tailnetCard contains all data for rendering a single tailnet card.
type tailnetCard struct {
	ID           int
	BaseDomain   string
	SocksAddr    string
	SocksHost    string
	SocksPort    string
	Machines     []machineView
	backendState string
	started      bool
	userEnabled  bool
	Hostname     string
	AuthURL      string
	ErrorMsg     string
}

func (c tailnetCard) StateClass() StateClass {
	if c.ErrorMsg != "" {
		return StateClassError
	}
	if !c.userEnabled || !c.started {
		return StateClassDisabled
	}
	switch c.backendState {
	case ipn.Running.String():
		return StateClassConnected
	case ipn.NeedsLogin.String():
		return StateClassNeedsLogin
	case ipn.NeedsMachineAuth.String():
		return StateClassNeedsAuth
	default:
		return StateClassConnecting
	}
}

func (c tailnetCard) IsToggleOn() bool {
	return c.userEnabled
}

func (c tailnetCard) ToggleAction() string {
	if c.IsToggleOn() {
		return "stop"
	}
	return "start"
}

func (c tailnetCard) IsErrorState() bool {
	return c.ErrorMsg != ""
}

// machineView represents a machine for display.
type machineView struct {
	Name        string
	DNSName     string
	StatusClass string
	StatusText  string
	IPs         string
}

// ServeDashboard renders the main dashboard page.
func ServeDashboard(w http.ResponseWriter, r *http.Request, reg *registry.Registry, listenAddr string) {
	data := dashboardData{PACFileURL: "http://" + listenAddr + pac.URLPath}
	tailnets := reg.List(r.Context())
	data.HasTailnets = len(tailnets) > 0
	for _, tailnet := range tailnets {
		if tailnet.MagicDNSSuffix == "" {
			data.HasUnconfiguredTailnets = true
		}
		data.Tailnets = append(data.Tailnets, buildTailnetCard(tailnet))
	}

	if err := renderTemplate(w, "dashboard.html", data); err != nil {
		slog.ErrorContext(r.Context(), "dashboard: failed to render template",
			slog.String("component", "dashboard"),
			slog.Any("error", err),
		)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func buildTailnetCard(tailnet registry.TailnetView) tailnetCard {
	baseDomain := "Tailnet"
	if tailnet.MagicDNSSuffix != "" {
		baseDomain = tailnet.MagicDNSSuffix
	}

	card := tailnetCard{
		ID:           tailnet.ID,
		BaseDomain:   baseDomain,
		backendState: tailnet.BackendState,
		started:      tailnet.Started,
		userEnabled:  tailnet.UserEnabled,
		Hostname:     tailnet.EffectiveHostname(),
		ErrorMsg:     tailnet.Error,
	}
	switch tailnet.BackendState {
	case ipn.NeedsLogin.String():
		card.AuthURL = tailnet.AuthURL
	case ipn.Running.String():
		card.SocksAddr = tailnet.SocksAddr()
		card.SocksHost, card.SocksPort, _ = net.SplitHostPort(card.SocksAddr)
		card.Machines = buildMachineViews(tailnet.Peers)
	}
	return card
}

func buildMachineViews(peers []registry.Peer) []machineView {
	var machines []machineView
	for _, peer := range peers {
		if len(peer.Addresses) == 0 {
			continue
		}
		status := "offline"
		if peer.Online {
			status = "online"
		}
		machines = append(machines, machineView{
			Name:        peer.Name,
			DNSName:     peer.DNSName,
			StatusClass: status,
			StatusText:  status,
			IPs:         strings.Join(formatIPs(peer.Addresses), ", "),
		})
	}
	sort.Slice(machines, func(i, j int) bool {
		if machines[i].StatusClass != machines[j].StatusClass {
			return machines[i].StatusClass == "online"
		}
		return strings.ToLower(machines[i].DNSName) < strings.ToLower(machines[j].DNSName)
	})
	return machines
}

func formatIPs(ips []netip.Prefix) []string {
	result := make([]string, len(ips))
	for i, ip := range ips {
		result[i] = ip.String()
	}
	return result
}
