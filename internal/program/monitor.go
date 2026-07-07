package program

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	sway "github.com/joshuarubin/go-sway"
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

// autoDetectSwayOutput returns the name of the first active sway output.
func autoDetectSwayOutput(ctx context.Context, client sway.Client) (string, error) {
	outputs, err := client.GetOutputs(ctx)
	if err != nil {
		return "", fmt.Errorf("get_outputs: %w", err)
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
