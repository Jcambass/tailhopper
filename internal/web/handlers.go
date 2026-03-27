package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jcambass/tailhopper/internal/registry"
	"github.com/jcambass/tailhopper/internal/sse"
	"github.com/jcambass/tailhopper/internal/tailscale"
	"github.com/jcambass/tailhopper/internal/ui"
)

// tailnetStartHandler handles POST /tailnet/{id}/start
func tailnetStartHandler(reg *registry.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		idStr := chi.URLParam(r, "id")

		id, err := strconv.Atoi(idStr)
		if err != nil {
			slog.WarnContext(ctx, "invalid tailnet id",
				slog.String("component", "httprequests"),
				slog.String("id", idStr),
			)
			http.Error(w, "invalid tailnet id", http.StatusBadRequest)
			return
		}

		tailnet, ok := reg.Get(id)
		if !ok {
			http.Error(w, "tailnet not found", http.StatusNotFound)
			return
		}

		if err := tailnet.Start(ctx); err != nil {
			slog.ErrorContext(ctx, "failed to start tailnet",
				slog.String("component", "httprequests"),
				slog.Any("error", err),
			)
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// tailnetStopHandler handles POST /tailnet/{id}/stop
func tailnetStopHandler(reg *registry.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		idStr := chi.URLParam(r, "id")

		id, err := strconv.Atoi(idStr)
		if err != nil {
			slog.WarnContext(ctx, "invalid tailnet id",
				slog.String("component", "httprequests"),
				slog.String("id", idStr),
			)
			http.Error(w, "invalid tailnet id", http.StatusBadRequest)
			return
		}

		tailnet, ok := reg.Get(id)
		if !ok {
			http.Error(w, "tailnet not found", http.StatusNotFound)
			return
		}

		if err := tailnet.Stop(ctx); err != nil {
			slog.ErrorContext(ctx, "failed to stop tailnet",
				slog.String("component", "httprequests"),
				slog.Any("error", err),
			)
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// logoutTimeout is the deadline for the logout call during tailnet deletion.
// Must be long enough for the tsnet server to start (if stopped) and complete logout.
const logoutTimeout = 10 * time.Second

// tailnetDeleteHandler handles DELETE /tailnet/{id}
func tailnetDeleteHandler(reg *registry.Registry, broadcaster *sse.SSEBroadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		idStr := chi.URLParam(r, "id")

		id, err := strconv.Atoi(idStr)
		if err != nil {
			slog.WarnContext(ctx, "invalid tailnet id",
				slog.String("component", "httprequests"),
				slog.String("id", idStr),
			)
			http.Error(w, "invalid tailnet id", http.StatusBadRequest)
			return
		}

		tailnet, ok := reg.Get(id)
		if !ok {
			slog.WarnContext(ctx, "tailnet not found",
				slog.String("component", "httprequests"),
				slog.Int("id", id),
			)
			http.Error(w, "tailnet not found", http.StatusNotFound)
			return
		}

		snapshot := tailnet.Snapshot()

		// Attempt logout unless the tailnet already has a terminal error.
		logoutErr := logoutBeforeDelete(ctx, tailnet, id, snapshot.State)

		// Always delete regardless of logout outcome.
		if err := reg.Delete(id); err != nil {
			slog.ErrorContext(ctx, "failed to delete tailnet",
				slog.String("component", "httprequests"),
				slog.Any("error", err),
			)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		slog.InfoContext(ctx, "tailnet deleted successfully",
			slog.String("component", "httprequests"),
		)
		broadcaster.BroadcastGlobalChange()

		// Return toast HTML using OOB swap with htmx auto-removal
		message, toastType := deleteResultMessage(snapshot.State, logoutErr)
		toastHTML, err := ui.RenderToast(toastType, message)
		if err != nil {
			slog.ErrorContext(ctx, "failed to render toast",
				slog.String("component", "httprequests"),
				slog.Any("error", err),
			)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, toastHTML)
	}
}

// logoutBeforeDelete attempts to log out unless the tailnet is in a terminal error state.
func logoutBeforeDelete(ctx context.Context, tailnet *tailscale.Tailnet, id int, state tailscale.State) error {
	if state == tailscale.HasTerminalErrorState {
		slog.InfoContext(ctx, "skipping logout for terminal-error tailnet",
			slog.String("component", "httprequests"),
			slog.Int("id", id),
		)
		return nil
	}

	logoutCtx, cancel := context.WithTimeout(ctx, logoutTimeout)
	defer cancel()

	if err := tailnet.Logout(logoutCtx); err != nil {
		slog.ErrorContext(ctx, "logout failed",
			slog.String("component", "httprequests"),
			slog.Int("id", id),
			slog.Any("error", err),
		)
		return err
	}

	slog.InfoContext(ctx, "logout succeeded",
		slog.String("component", "httprequests"),
		slog.Int("id", id),
	)
	return nil
}

// deleteResultMessage returns the toast message and type for a tailnet deletion.
func deleteResultMessage(state tailscale.State, logoutErr error) (string, string) {
	if state == tailscale.HasTerminalErrorState {
		return "Tailnet deleted successfully", "success"
	}
	if logoutErr == nil {
		return "Tailnet deleted and logged out successfully", "success"
	}
	return fmt.Sprintf("Tailnet deleted, but logout failed: %s", logoutErr.Error()), "warning"
}

// addTailnetHandler handles POST /tailnet/add for creating a new tailnet.
func addTailnetHandler(reg *registry.Registry, sseBroadcast *sse.SSEBroadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Check if there are any unconfigured tailnets
		if reg.HasUnconfiguredTailnets() {
			slog.WarnContext(ctx, "cannot add new tailnet: unconfigured tailnet exists",
				slog.String("component", "httprequests"),
			)
			http.Error(w, "cannot create new tailnet while an existing tailnet is unconfigured", http.StatusConflict)
			return
		}

		tailnet, err := reg.Add("") // Empty hostname will be auto-generated by registry
		if err != nil {
			slog.ErrorContext(ctx, "failed to add tailnet",
				slog.String("component", "httprequests"),
				slog.Any("error", err),
			)
			http.Error(w, fmt.Sprintf("failed to add tailnet: %v", err), http.StatusBadRequest)
			return
		}

		// Automatically start the new tailnet
		if err := tailnet.Start(r.Context()); err != nil {
			slog.ErrorContext(ctx, "failed to start new tailnet",
				slog.String("component", "httprequests"),
				slog.Any("error", err),
			)
			// Don't fail the request, the tailnet was created successfully
		}

		// Notify about new tailnet (registry also sends notification, but this ensures immediate update)
		sseBroadcast.BroadcastGlobalChange()

		w.WriteHeader(http.StatusCreated)
	}
}
