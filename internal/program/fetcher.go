package program

import (
	"context"
	"fmt"

	sway "github.com/joshuarubin/go-sway"
)

// WorkspaceFetcher retrieves the current workspace list from the window manager.
type WorkspaceFetcher interface {
	FetchWorkspaces(ctx context.Context) ([]Workspace, error)
}

// compile-time check
var _ WorkspaceFetcher = (*swayFetcher)(nil)

// swayFetcher is the WorkspaceFetcher backed by go-sway's IPC client.
type swayFetcher struct {
	client sway.Client
}

func (f *swayFetcher) FetchWorkspaces(ctx context.Context) ([]Workspace, error) {
	swss, err := f.client.GetWorkspaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("get_workspaces: %w", err)
	}
	wss := make([]Workspace, len(swss))
	for i, w := range swss {
		wss[i] = Workspace{
			Name:    w.Name,
			Num:     int(w.Num),
			Focused: w.Focused,
			Urgent:  w.Urgent,
			Output:  w.Output,
		}
	}
	return wss, nil
}
