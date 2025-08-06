package program

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
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

// fetchWorkspaces retrieves workspaces using the detected command.
func fetchWorkspaces(ctx context.Context, cmdName string) ([]Workspace, error) {
	cmd := exec.CommandContext(ctx, cmdName, "-t", "get_workspaces")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s get_workspaces: %w", cmdName, err)
	}
	var wss []Workspace
	if err := json.Unmarshal(out, &wss); err != nil {
		return nil, fmt.Errorf("unmarshal workspaces JSON: %w", err)
	}
	return wss, nil
}

// render builds and prints the EWW widget for the given output.
func render(cmdName, output string) error {
	states := make([]string, endWS+1)
	visible := make([]bool, endWS+1)
	for i := startWS; i <= endWS; i++ {
		states[i] = "unoccupied"
		visible[i] = true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	wss, err := fetchWorkspaces(ctx, cmdName)
	if err != nil {
		return err
	}

	for _, ws := range wss {
		if ws.Output != output {
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
		visible[ws.Num] = true
	}

	parts := make([]string, 0, endWS)
	for i := startWS; i <= endWS; i++ {
		parts = append(parts, fmt.Sprintf(btnFormat, detectCommand(), i, visible[i], states[i], i))
	}
	widget := fmt.Sprintf(ewwFormat, strings.Join(parts, " "))
	fmt.Println(widget)
	return nil
}

// subscribeAndRender handles initial render and i3/sway subscriptions.
func subscribeAndRender(monitor, file string) error {
	cmdName := detectCommand()

	// initial render
	execCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := readMonitorOutput(execCtx, file, monitor)
	if err != nil {
		return err
	}
	if err := render(cmdName, output); err != nil {
		log.Println("initial render error:", err)
	}

	// subscribe to events
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
		if err := render(cmdName, output); err != nil {
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
	// Print the PATH as seen by the Go program:
	fmt.Println("PATH:", os.Getenv("PATH"))
	// first try swaymsg
	if swayPath, err := exec.LookPath("swaymsg"); err == nil {
		// verify it really is a sway instance
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if err := exec.CommandContext(ctx, swayPath, "-t", "get_version").Run(); err == nil {
			return swayPath
		}
	}
	// fallback to i3-msg
	if i3Path, err := exec.LookPath("i3-msg"); err == nil {
		return i3Path
	}
	// last resort, just the name (will error later if not on PATH)
	return "i3-msg"
}

// Run sets up and starts the subscription-render loop.
func Run(ctx context.Context) {
	monitor := flag.String("monitor", "", "monitor name to display workspaces for")
	file := flag.String("monitors-file", "/tmp/monitors.json", "path to monitor JSON file")
	flag.Parse()

	if *monitor == "" {
		flag.Usage()
		os.Exit(1)
	}

	if err := subscribeAndRender(*monitor, *file); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			log.Fatalf("command exited with error: %v", err)
		}
		log.Fatalf("error: %v", err)
	}
}
