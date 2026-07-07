package program

const (
	startWS = 1
	endWS   = 10
)

type wsView struct {
	N     int    `json:"n"`
	State string `json:"state"`
}

type payload struct {
	Focused int      `json:"focused"`
	List    []wsView `json:"list"`
}

// buildPayload maps workspaces onto per-slot states for the eww consumer.
// Workspaces on a different output are ignored. Workspace numbers outside
// [startWS, endWS] are ignored (prevents out-of-bounds on the list slice).
func buildPayload(workspaces []Workspace, output string) payload {
	list := make([]wsView, endWS-startWS+1)
	for i := range list {
		list[i] = wsView{N: startWS + i, State: "unoccupied"}
	}

	focused := 0
	for _, ws := range workspaces {
		if ws.Output != output {
			continue
		}
		if ws.Num < startWS || ws.Num > endWS {
			continue
		}
		idx := ws.Num - startWS
		switch {
		case ws.Urgent:
			list[idx].State = "urgent"
		case ws.Focused:
			list[idx].State = "focused"
			focused = ws.Num
		default:
			list[idx].State = "occupied"
		}
	}
	return payload{Focused: focused, List: list}
}
