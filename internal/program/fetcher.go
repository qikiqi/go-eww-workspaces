package program

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

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
