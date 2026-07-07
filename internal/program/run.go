package program

import (
	"context"
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

// render fetches workspaces and writes the EWW widget string to w.
func render(ctx context.Context, w io.Writer, fetcher WorkspaceFetcher, output string) error {
	fetchCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	wss, err := fetcher.FetchWorkspaces(fetchCtx)
	if err != nil {
		return err
	}

	fmt.Fprintln(w, buildWidget(wss, output))
	return nil
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
type eventHandler struct {
	sway.EventHandler
	fetcher WorkspaceFetcher
	writer  io.Writer
	output  string
}

func (h eventHandler) Workspace(ctx context.Context, _ sway.WorkspaceEvent) {
	if err := render(ctx, h.writer, h.fetcher, h.output); err != nil {
		slog.Error("render failed", "err", err)
	}
}

func (h eventHandler) Window(ctx context.Context, e sway.WindowEvent) {
	if !shouldRerenderWindow(e.Change) {
		return
	}
	if err := render(ctx, h.writer, h.fetcher, h.output); err != nil {
		slog.Error("render failed", "err", err)
	}
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

	if err := render(ctx, os.Stdout, fetcher, output); err != nil {
		slog.Error("initial render failed", "err", err)
	}

	handler := eventHandler{
		EventHandler: sway.NoOpEventHandler(),
		fetcher:      fetcher,
		writer:       os.Stdout,
		output:       output,
	}
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
