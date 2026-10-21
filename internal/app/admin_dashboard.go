package app

import (
	"encoding/json"
	"net/http"
)

func registerDashboardRoutes(adminMux *http.ServeMux) {
	adminMux.HandleFunc("/admin/api/dashboard/summary", corsHandler(handleDashboardSummary))
}

// GET /admin/api/dashboard/summary
func handleDashboardSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	data := dashboardSummary()
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: data})
}

// extractUsageFromResponse 从响应体 JSON 中提取 prompt_tokens 和 completion_tokens。
// 用于中间件层在请求完成后记录指标。流式响应（SSE）的 body 不含顶层 usage，返回 0,0。
func extractUsageFromResponse(body []byte) (promptTokens, completionTokens int64) {
	if len(body) == 0 {
		return 0, 0
	}
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return 0, 0
	}
	u, ok := m["usage"].(map[string]any)
	if !ok || u == nil {
		return 0, 0
	}
	if v, ok := u["prompt_tokens"].(float64); ok {
		promptTokens = int64(v)
	}
	if v, ok := u["completion_tokens"].(float64); ok {
		completionTokens = int64(v)
	}
	return promptTokens, completionTokens
}
