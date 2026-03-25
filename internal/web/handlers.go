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
	"github.com/jcambass/tailhopper/internal/ts"
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

		var logoutErr error

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
		if snapshot.State == ts.HasTerminalErrorState {
			slog.InfoContext(r.Context(), "skipping logout for terminal-error tailnet",
				slog.String("component", "httprequests"),
				slog.Int("id", id),
			)
		} else {
			// Give logout a deadline to prevent hanging the request indefinitely.
			// Note that logout might internally start the tsnet server if it's not already started,
			// so we need to give it enough time to do that and complete the logout process.
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			err = tailnet.Logout(ctx)
			if err != nil {
				slog.ErrorContext(r.Context(), "logout failed",
					slog.String("component", "httprequests"),
					slog.Int("id", id),
					slog.Any("error", err),
				)
				logoutErr = err
			} else {
				slog.InfoContext(r.Context(), "logout succeeded",
					slog.String("component", "httprequests"),
					slog.Int("id", id),
				)
			}
		}

		// Always delete regardless of logout success
		if err := reg.Delete(id); err != nil {
			slog.ErrorContext(r.Context(), "failed to delete tailnet",
				slog.String("component", "httprequests"),
				slog.Any("error", err),
			)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		slog.InfoContext(r.Context(), "tailnet deleted successfully",
			slog.String("component", "httprequests"),
		)
		broadcaster.BroadcastGlobalChange()

		// Return toast HTML using OOB swap with htmx auto-removal
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)

		var message, toastType string
		if snapshot.State == ts.HasTerminalErrorState {
			message = "Tailnet deleted successfully"
			toastType = "success"
		} else if logoutErr == nil {
			message = "Tailnet deleted and logged out successfully"
			toastType = "success"
		} else {
			message = fmt.Sprintf("Tailnet deleted, but logout failed: %s", logoutErr.Error())
			toastType = "warning"
		}

		toastHTML, err := ui.RenderToast(toastType, message)
		if err != nil {
			slog.ErrorContext(r.Context(), "failed to render toast",
				slog.String("component", "httprequests"),
				slog.Any("error", err),
			)
			return
		}
		fmt.Fprint(w, toastHTML)
	}
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
