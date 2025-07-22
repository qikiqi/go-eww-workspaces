package program

import (
	"bufio"
	"context"
	"encoding/json"
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
	btnFormat = `(button :onclick "i3-msg 'workspace %d'" :visible %t :class "%s" "%d")`
)

type MonitorInfo struct {
	Monitor string `json:"monitor"`
	Output  string `json:"output"`
}

type I3Workspace struct {
	Name    string `json:"name"`
	Num     int    `json:"num"`
	Focused bool   `json:"focused"`
	Urgent  bool   `json:"urgent"`
	Output  string `json:"output"`
}

// waitForReadableFile retries os.ReadFile until it succeeds (and returns
// non‑empty data), or ctx is done.
func waitForReadableFile(ctx context.Context, path string, interval time.Duration) ([]byte, error) {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return nil, fmt.Errorf("timeout waiting for readable file %q: %w", path, ctx.Err())
        case <-ticker.C:
            data, err := os.ReadFile(path)
            if err == nil && len(data) > 0 {
                return data, nil
            }
            // otherwise keep retrying
        }
    }
}

// ReadMonitorOutput waits up to timeout for the JSON file at path
// to appear, be readable, and contain valid JSON.  It then looks for
// the given monitor name and returns its Output.
func ReadMonitorOutput(path, monitor string, timeout time.Duration) (string, error) {
    // set up a context that times out after timeout
    ctx, cancel := context.WithTimeout(context.Background(), timeout)
    defer cancel()

    // 1. Wait for the file to be readable and non‑empty
    data, err := waitForReadableFile(ctx, path, 200*time.Millisecond)
    if err != nil {
        return "", err
    }

    // 2. Retry JSON unmarshalling until valid or timeout
    var infos []MonitorInfo
    ticker := time.NewTicker(200 * time.Millisecond)
    defer ticker.Stop()

    for {
        if err := json.Unmarshal(data, &infos); err == nil {
            break // valid JSON
        }
        select {
        case <-ctx.Done():
            return "", fmt.Errorf("timeout parsing monitors JSON %q: %w", path, ctx.Err())
        case <-ticker.C:
            data, err = os.ReadFile(path)
            if err != nil {
                // maybe someone truncated it, so keep retrying
                continue
            }
        }
    }

    // 3. Find the requested monitor entry
    for _, mi := range infos {
        if mi.Monitor == monitor {
            return mi.Output, nil
        }
    }

    return "", fmt.Errorf("monitor %q not found in %q", monitor, path)
}

func fetchWorkspaces(ctx context.Context) ([]I3Workspace, error) {
	cmd := exec.CommandContext(ctx, "i3-msg", "-t", "get_workspaces")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("i3-msg get_workspaces: %w", err)
	}
	var wss []I3Workspace
	if err := json.Unmarshal(out, &wss); err != nil {
		return nil, fmt.Errorf("parse i3 workspaces JSON: %w", err)
	}
	return wss, nil
}

func render(output string) error {
	// prepare default states
	states := make([]string, endWS+1)
	vis := make([]bool, endWS+1)
	for i := startWS; i <= endWS; i++ {
		states[i] = "unoccupied"
		vis[i] = true
	}

	// get current workspaces
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	wss, err := fetchWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, ws := range wss {
		if ws.Output != output {
			continue
		}
		n := ws.Num
		switch {
		case ws.Urgent:
			states[n] = "urgent"
			vis[n] = true
		case ws.Focused:
			states[n] = "focused"
			vis[n] = true
		default:
			states[n] = "occupied"
			vis[n] = true
		}
	}

	// build buttons
	var parts []string
	for i := startWS; i <= endWS; i++ {
		parts = append(parts,
			fmt.Sprintf(btnFormat,
				i,
				vis[i],
				states[i],
				i,
			),
		)
	}
	widget := fmt.Sprintf(ewwFormat, strings.Join(parts, " "))
	fmt.Println(widget)
	return nil
}

func subscribeAndRender(monitor, monitorsFile string) {
	// initial
	output, err := ReadMonitorOutput(monitorsFile, monitor, 5*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	if err := render(output); err != nil {
		log.Println("initial render error:", err)
	}

	// subscribe
	cmd := exec.Command("i3-msg", "-t", "subscribe", "-m", `["window","workspace"]`)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}

	s := bufio.NewScanner(stdout)
	for s.Scan() {
		if err := render(output); err != nil {
			log.Println("render error:", err)
		}
	}
	if err := s.Err(); err != nil {
		log.Fatal(err)
	}
}


func Run(ctx context.Context) {
	monitor := flag.String("monitor", "", "name of the monitor to display workspaces for")
	file := flag.String("monitors-file", "/tmp/monitors.json", "path to monitors JSON file")
	flag.Parse()
	if *monitor == "" {
		flag.Usage()
		os.Exit(1)
	}
	subscribeAndRender(*monitor, *file)
}
