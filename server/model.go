package server

type ResponseModel struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

type CreatePipelineModel struct {
	Source map[string]interface{} `json:"source"`
	Sink   map[string]interface{} `json:"sink"`
}
