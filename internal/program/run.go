package program

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	sway "github.com/joshuarubin/go-sway"

	"github.com/qikiqi/go-eww-workspaces/internal/version"
)

// renderPayload fetches workspaces and encodes them as a JSON line
// (payload object + trailing '\n') suitable for eww's deflisten.
func renderPayload(ctx context.Context, fetcher WorkspaceFetcher, output string) ([]byte, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	wss, err := fetcher.FetchWorkspaces(fetchCtx)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(buildPayload(wss, output)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// shouldRerenderWindow reports whether a window event's change type can
// affect the workspace widget (i.e. workspace occupancy).
func shouldRerenderWindow(change sway.WindowEventChange) bool {
	switch change {
	case sway.WindowNew, sway.WindowClose, sway.WindowMove:
		return true
	default:
		return false
	}
}

// eventHandler drives re-renders in response to sway subscribe events.
// Skips writes when the newly-rendered payload matches the previous one,
// so no-op events (e.g. window::move within a workspace whose occupancy
// didn't change) don't hit the stdout pipe or eww's parser.
type eventHandler struct {
	sway.EventHandler
	fetcher WorkspaceFetcher
	writer  io.Writer
	output  string
	last    []byte
}

// emit renders the current state and writes it if it differs from the
// previous emission. Errors are logged, not returned — the subscription
// loop must keep running.
func (h *eventHandler) emit(ctx context.Context) {
	next, err := renderPayload(ctx, h.fetcher, h.output)
	if err != nil {
		slog.Error("render failed", "err", err)
		return
	}
	if bytes.Equal(next, h.last) {
		return
	}
	if _, err := h.writer.Write(next); err != nil {
		slog.Error("write failed", "err", err)
		return
	}
	h.last = append(h.last[:0], next...)
}

func (h *eventHandler) Workspace(ctx context.Context, _ sway.WorkspaceEvent) {
	h.emit(ctx)
}

func (h *eventHandler) Window(ctx context.Context, e sway.WindowEvent) {
	if !shouldRerenderWindow(e.Change) {
		return
	}
	h.emit(ctx)
}

// subscribeAndRender handles initial render and sway subscription.
func subscribeAndRender(ctx context.Context, monitor, file string) error {
	client, err := sway.New(ctx)
	if err != nil {
		return fmt.Errorf("connect to sway: %w", err)
	}
	fetcher := &swayFetcher{client: client}

	execCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var output string
	if monitor == "" {
		output, err = autoDetectSwayOutput(execCtx, client)
	} else {
		output, err = readMonitorOutput(execCtx, file, monitor)
	}
	if err != nil {
		return err
	}

	handler := &eventHandler{
		EventHandler: sway.NoOpEventHandler(),
		fetcher:      fetcher,
		writer:       os.Stdout,
		output:       output,
	}
	handler.emit(ctx)

	if err := sway.Subscribe(ctx, handler, sway.EventTypeWorkspace, sway.EventTypeWindow); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return fmt.Errorf("sway subscribe: %w", err)
	}
	return nil
}

// Run sets up and starts the subscription-render loop.
func Run(ctx context.Context) {
	monitor := flag.String("monitor", "", "monitor name to display workspaces for, empty for autodetect")
	file := flag.String("monitors-file", "/tmp/monitors.json", "path to monitor JSON file")
	versionFlag := flag.Bool("version", false, "print version and exit")
	versionFlagShort := flag.Bool("v", false, "print version and exit (shorthand)")
	flag.Parse()

	if *versionFlag || *versionFlagShort {
		if err := version.Print(); err != nil {
			slog.Error("version info unavailable", "err", err)
			os.Exit(1)
		}
		return
	}

	if err := subscribeAndRender(ctx, *monitor, *file); err != nil {
		slog.Error("fatal error", "err", err)
		os.Exit(1)
	}
}
