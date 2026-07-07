package program

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
