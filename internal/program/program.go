package program

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/qikiqi/go-eww-workspaces/internal/version"
)

const (
	startWS   = 1
	endWS     = 10
	ewwFormat = `(box :class "workspaces" :orientation "h" :halign "start" :spacing "6" :space-evenly "true" %s)`
	btnFormat = `(button :onclick "%s 'workspace %d'" :visible %t :class "%s" "%d")`
)

type MonitorInfo struct {
	Monitor string `json:"monitor"`
	Output  string `json:"output"`
}

type Workspace struct {
	Name    string `json:"name"`
	Num     int    `json:"num"`
	Focused bool   `json:"focused"`
	Urgent  bool   `json:"urgent"`
	Output  string `json:"output"`
}

// WorkspaceFetcher retrieves the current workspace list from the window manager.
type WorkspaceFetcher interface {
	FetchWorkspaces(ctx context.Context) ([]Workspace, error)
}

// compile-time check
var _ WorkspaceFetcher = (*commandFetcher)(nil)

// commandFetcher is the real WorkspaceFetcher backed by swaymsg/i3-msg.
type commandFetcher struct {
	cmdName string
}

func (f *commandFetcher) FetchWorkspaces(ctx context.Context) ([]Workspace, error) {
	cmd := exec.CommandContext(ctx, f.cmdName, "-t", "get_workspaces")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s get_workspaces: %w", f.cmdName, err)
	}
	var wss []Workspace
	if err := json.Unmarshal(out, &wss); err != nil {
		return nil, fmt.Errorf("unmarshal workspaces JSON: %w", err)
	}
	return wss, nil
}

// waitForFile polls until the file at path is readable and non-empty, or context done.
func waitForFile(ctx context.Context, path string, interval time.Duration) ([]byte, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for file %s: %w", path, ctx.Err())
		case <-ticker.C:
			data, err := os.ReadFile(path)
			if err == nil && len(data) > 0 {
				return data, nil
			}
		}
	}
}

// autoDetectMonitorOutput runs `swaymsg -t get_outputs` and returns the output
// string for the first active monitor, formatted the same way as readMonitorOutput.
func autoDetectMonitorOutput(ctx context.Context) (string, error) {
	type swayOutput struct {
		Name   string `json:"name"`
		Active bool   `json:"active"`
	}

	cmd := exec.CommandContext(ctx, "swaymsg", "-t", "get_outputs")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to run swaymsg: %w", err)
	}

	var outputs []swayOutput
	if err := json.Unmarshal(out.Bytes(), &outputs); err != nil {
		return "", fmt.Errorf("failed to parse sway outputs JSON: %w", err)
	}

	for _, o := range outputs {
		if o.Active {
			return o.Name, nil
		}
	}

	return "", fmt.Errorf("no active monitor found")
}

// readMonitorOutput reads JSON array from file and returns output for given monitor.
func readMonitorOutput(ctx context.Context, path, monitor string) (string, error) {
	data, err := waitForFile(ctx, path, 200*time.Millisecond)
	if err != nil {
		return "", err
	}

	var infos []MonitorInfo
	for {
		if err := json.Unmarshal(data, &infos); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("parsing JSON %s: %w", path, ctx.Err())
		case <-time.After(200 * time.Millisecond):
			data, _ = os.ReadFile(path)
		}
	}

	for _, mi := range infos {
		if mi.Monitor == monitor {
			return mi.Output, nil
		}
	}
	return "", fmt.Errorf("monitor %q not found in %s", monitor, path)
}

// buildWidget maps workspaces onto button states and returns the EWW widget string.
// Workspaces on a different output are ignored. Workspace numbers outside [startWS, endWS]
// are ignored (prevents out-of-bounds on the state slices).
func buildWidget(workspaces []Workspace, output, cmdName string) string {
	states := make([]string, endWS+1)
	visible := make([]bool, endWS+1)
	for i := startWS; i <= endWS; i++ {
		states[i] = "unoccupied"
		visible[i] = true
	}

	for _, ws := range workspaces {
		if ws.Output != output {
			continue
		}
		if ws.Num < startWS || ws.Num > endWS {
			continue
		}
		switch {
		case ws.Urgent:
			states[ws.Num] = "urgent"
		case ws.Focused:
			states[ws.Num] = "focused"
		default:
			states[ws.Num] = "occupied"
		}
	}

	parts := make([]string, 0, endWS)
	for i := startWS; i <= endWS; i++ {
		parts = append(parts, fmt.Sprintf(btnFormat, cmdName, i, visible[i], states[i], i))
	}
	return fmt.Sprintf(ewwFormat, strings.Join(parts, " "))
}

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
func subscribeAndRender(monitor, file string) error {
	cmdName := detectCommand()
	fetcher := &commandFetcher{cmdName: cmdName}

	execCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var output string
	var err error
	if monitor == "" {
		output, err = autoDetectMonitorOutput(execCtx)
	} else {
		output, err = readMonitorOutput(execCtx, file, monitor)
	}
	if err != nil {
		return err
	}

	if err := render(context.Background(), os.Stdout, fetcher, cmdName, output); err != nil {
		log.Println("initial render error:", err)
	}

	subCmd := exec.Command(cmdName, "-t", "subscribe", "-m", `["window","workspace"]`)
	stdout, err := subCmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := subCmd.Start(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if err := render(context.Background(), os.Stdout, fetcher, cmdName, output); err != nil {
			log.Println("render error:", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

// detectCommand returns "swaymsg" if it successfully detects sway, otherwise "i3-msg".
func detectCommand() string {
	if swayPath, err := exec.LookPath("swaymsg"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if err := exec.CommandContext(ctx, swayPath, "-t", "get_version").Run(); err == nil {
			return swayPath
		}
	}
	if i3Path, err := exec.LookPath("i3-msg"); err == nil {
		return i3Path
	}
	return "i3-msg"
}

// Run sets up and starts the subscription-render loop.
func Run(ctx context.Context) {
	monitor := flag.String("monitor", "", "monitor name to display workspaces for, empty for autodetect")
	file := flag.String("monitors-file", "/tmp/monitors.json", "path to monitor JSON file")
	versionFlag := flag.Bool("version", false, "print version and exit")
	versionFlagShort := flag.Bool("v", false, "print version and exit (shorthand)")
	flag.Parse()

	if *versionFlag || *versionFlagShort {
		version.Print()
		return
	}

	if err := subscribeAndRender(*monitor, *file); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			log.Fatalf("command exited with error: %v", err)
		}
		log.Fatalf("error: %v", err)
	}
}
