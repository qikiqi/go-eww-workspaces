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
	"sync"
	"time"

	sway "github.com/joshuarubin/go-sway"

	"github.com/qikiqi/go-eww-workspaces/internal/version"
)

// debounceInterval is the trailing-edge coalescing window for sway event
// bursts. 16 ms is one frame at 60 Hz — imperceptible to the user, long
// enough to fold typical tiling bursts (window::new + window::move +
// workspace::focus arriving within a few milliseconds) into a single emit.
const debounceInterval = 16 * time.Millisecond

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
//
// Sway events are funneled through a trailing-edge debouncer so a burst
// of updates within debounce collapses to one emit. Combined with the
// payload-dedup check in emit, the eww consumer sees exactly the visible
// state changes and none of the intermediate churn.
type eventHandler struct {
	sway.EventHandler
	fetcher  WorkspaceFetcher
	writer   io.Writer
	output   string
	last     []byte
	signal   chan struct{}
	debounce time.Duration
}

// newEventHandler wires an event handler with a size-1 signal channel and
// the given debounce window. Callers must run runDebouncer in a goroutine.
func newEventHandler(fetcher WorkspaceFetcher, w io.Writer, output string, debounce time.Duration) *eventHandler {
	return &eventHandler{
		EventHandler: sway.NoOpEventHandler(),
		fetcher:      fetcher,
		writer:       w,
		output:       output,
		signal:       make(chan struct{}, 1),
		debounce:     debounce,
	}
}

// kick requests a debounced re-emit. Non-blocking: if a request is
// already queued, the extra kick is dropped (the debouncer will pick
// up the latest state when its window fires).
func (h *eventHandler) kick() {
	select {
	case h.signal <- struct{}{}:
	default:
	}
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

// runDebouncer consumes signals and calls emit at most once per debounce
// window. A kick starts the timer; further kicks reset it; when the timer
// fires, one emit happens. Exits when ctx is cancelled.
func (h *eventHandler) runDebouncer(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.signal:
			timer := time.NewTimer(h.debounce)
		waiting:
			for {
				select {
				case <-ctx.Done():
					if !timer.Stop() {
						<-timer.C
					}
					return
				case <-h.signal:
					if !timer.Stop() {
						<-timer.C
					}
					timer.Reset(h.debounce)
				case <-timer.C:
					break waiting
				}
			}
			h.emit(ctx)
		}
	}
}

func (h *eventHandler) Workspace(_ context.Context, _ sway.WorkspaceEvent) {
	h.kick()
}

func (h *eventHandler) Window(_ context.Context, e sway.WindowEvent) {
	if !shouldRerenderWindow(e.Change) {
		return
	}
	h.kick()
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

	handler := newEventHandler(fetcher, os.Stdout, output, debounceInterval)

	// The initial emit is synchronous and bypasses the debouncer — eww's
	// deflisten should have a value to render as soon as the daemon starts.
	handler.emit(ctx)

	// Debouncer runs under a derived context so we can stop it before
	// returning, ensuring the goroutine has exited (goleak-clean).
	dctx, cancelD := context.WithCancel(ctx)
	defer cancelD()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handler.runDebouncer(dctx)
	}()

	subErr := sway.Subscribe(ctx, handler, sway.EventTypeWorkspace, sway.EventTypeWindow)
	cancelD()
	wg.Wait()

	if subErr != nil {
		if errors.Is(subErr, context.Canceled) || errors.Is(subErr, context.DeadlineExceeded) {
			return nil
		}
		return fmt.Errorf("sway subscribe: %w", subErr)
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
