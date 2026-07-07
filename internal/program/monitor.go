package program

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

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

// autoDetectMonitorOutput runs `<cmd> -t get_outputs` and returns the output
// string for the first active monitor, formatted the same way as readMonitorOutput.
func autoDetectMonitorOutput(ctx context.Context, cmdName string) (string, error) {
	type swayOutput struct {
		Name   string `json:"name"`
		Active bool   `json:"active"`
	}

	cmd := exec.CommandContext(ctx, cmdName, "-t", "get_outputs")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s get_outputs: %w", cmdName, err)
	}

	var outputs []swayOutput
	if err := json.Unmarshal(out.Bytes(), &outputs); err != nil {
		return "", fmt.Errorf("parse sway outputs JSON: %w", err)
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
			if d, err := os.ReadFile(path); err == nil {
				data = d
			}
		}
	}

	for _, mi := range infos {
		if mi.Monitor == monitor {
			return mi.Output, nil
		}
	}
	return "", fmt.Errorf("monitor %q not found in %s", monitor, path)
}
