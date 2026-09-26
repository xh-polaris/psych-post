package internalapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/xh-polaris/psych-post/biz/domain/report"
	"github.com/xh-polaris/psych-post/pkg/logs"
)

const maxReportRequestBytes = 2 << 20

const upstreamHeader = "X-Psych-Upstream"

type GenerateReport func(context.Context, report.OpenAPIReportRequest, string) (*report.OpenAPIReportResult, error)

// NewReportHandler 创建仅供 core-api 调用的同步报告生成入口
func NewReportHandler(token string, generate GenerateReport) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		if !validToken(r.Header.Get("Authorization"), token) {
			writeError(w, http.StatusUnauthorized, "invalid_internal_token")
			return
		}
		if generate == nil {
			writeError(w, http.StatusServiceUnavailable, "report_generator_unavailable")
			return
		}
		upstream := strings.TrimSpace(r.Header.Get(upstreamHeader))
		if upstream == "" {
			writeError(w, http.StatusBadRequest, "missing_upstream")
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxReportRequestBytes))
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large")
			return
		}
		var req report.OpenAPIReportRequest
		if err := sonic.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		startedAt := time.Now()
		result, err := generate(r.Context(), req, upstream)
		if err != nil {
			status, code := reportError(err)
			logs.CtxErrorf(r.Context(), "[internal report] request_id=%s upstream=%s status=%s err=%v", req.RequestID, upstream, code, err)
			logs.CtxInfof(r.Context(), "[internal report] request_id=%s upstream=%s status=%s duration_ms=%d", req.RequestID, upstream, code, time.Since(startedAt).Milliseconds())
			writeError(w, status, code)
			return
		}
		logs.CtxInfof(r.Context(), "[internal report] request_id=%s upstream=%s status=completed duration_ms=%d", req.RequestID, upstream, time.Since(startedAt).Milliseconds())
		writeJSON(w, http.StatusOK, result)
	})
}

func validToken(header, token string) bool {
	if token == "" {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if provided == "" || strings.TrimSpace(header) == provided {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
}

func reportError(err error) (int, string) {
	if errors.Is(err, report.ErrInvalidOpenAPIReportRequest) {
		return http.StatusBadRequest, "invalid_request"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout, "upstream_timeout"
	}
	return http.StatusBadGateway, "upstream_error"
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := sonic.Marshal(value)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
