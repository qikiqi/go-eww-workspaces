package program

type MonitorInfo struct {
	Monitor string `json:"monitor"`
	Output  string `json:"output"`
}

type Workspace struct {
	Name    string
	Num     int
	Focused bool
	Urgent  bool
	Output  string
}
