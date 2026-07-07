package program

import (
	"fmt"
	"strings"
)

const (
	startWS   = 1
	endWS     = 10
	ewwFormat = `(box :class "workspaces" :orientation "h" :halign "start" :spacing "6" :space-evenly "true" %s)`
	btnFormat = `(button :onclick "%s 'workspace %d'" :visible %t :class "%s" "%d")`
)

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
