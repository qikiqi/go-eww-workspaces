package program

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/qikiqi/go-eww-workspaces/internal/version"
)

// render fetches workspaces and writes the EWW widget string to w.
func render(ctx context.Context, w io.Writer, fetcher WorkspaceFetcher, cmdName, output string) error {
	fetchCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	wss, err := fetcher.FetchWorkspaces(fetchCtx)
	if err != nil {
		return err
	}

	fmt.Fprintln(w, buildWidget(wss, output, cmdName))
	return nil
}

// subscribeAndRender handles initial render and i3/sway subscriptions.
func subscribeAndRender(ctx context.Context, monitor, file string) error {
	cmdName := detectCommand()
	fetcher := &commandFetcher{cmdName: cmdName}

	execCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var output string
	var err error
	if monitor == "" {
		output, err = autoDetectMonitorOutput(execCtx, cmdName)
	} else {
		output, err = readMonitorOutput(execCtx, file, monitor)
	}
	if err != nil {
		return err
	}

	if err := render(ctx, os.Stdout, fetcher, cmdName, output); err != nil {
		slog.Error("initial render failed", "err", err)
	}

	subCmd := exec.CommandContext(ctx, cmdName, "-t", "subscribe", "-m", `["window","workspace"]`)
	stdout, err := subCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe for subscribe: %w", err)
	}
	if err := subCmd.Start(); err != nil {
		return fmt.Errorf("start subscribe command: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		if err := render(ctx, os.Stdout, fetcher, cmdName, output); err != nil {
			slog.Error("render failed", "err", err)
		}
	}
	if err := scanner.Err(); err != nil {
		_ = subCmd.Wait()
		return fmt.Errorf("subscribe scanner: %w", err)
	}
	if err := subCmd.Wait(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("subscribe command: %w", err)
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
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			slog.Error("command exited with error", "err", err)
		} else {
			slog.Error("fatal error", "err", err)
		}
		os.Exit(1)
	}
}
