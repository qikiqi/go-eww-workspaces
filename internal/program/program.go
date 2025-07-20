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

func readMonitors(path, monitor string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read monitors file: %w", err)
	}
	var infos []MonitorInfo
	if err := json.Unmarshal(data, &infos); err != nil {
		return "", fmt.Errorf("parse monitors JSON: %w", err)
	}
	for _, mi := range infos {
		if mi.Monitor == monitor {
			return mi.Output, nil
		}
	}
	return "", fmt.Errorf("monitor %q not found", monitor)
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
	output, err := readMonitors(monitorsFile, monitor)
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
