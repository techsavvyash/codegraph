package main

func errorResponse(msg string) ToolCallResponse {
	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}
