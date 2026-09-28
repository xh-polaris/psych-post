package report

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"text/template"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/xh-polaris/psych-post/biz/conf"
	"github.com/xh-polaris/psych-post/biz/domain/prompt"
	_ "github.com/xh-polaris/psych-post/biz/infra/llm"
	impl "github.com/xh-polaris/psych-post/biz/infra/llm/impl"
	re "github.com/xh-polaris/psych-post/biz/infra/mapper/report"
	"github.com/xh-polaris/psych-post/pkg/app"
	"github.com/xh-polaris/psych-post/pkg/logs"
)

const (
	defaultOpenAPIReportMaxTokens = 4096
	maxOpenAPIReportMaxTokens     = 4096
	maxOpenAPIReportAttempts      = 2
)

var (
	ErrInvalidOpenAPIReportRequest  = errors.New("invalid openapi report request")
	errOpenAPIReportMalformedOutput = errors.New("openapi report model output is malformed")
)

const openAPIReportRepairInstruction = "\n\n上一次输出未能解析为完整报告。请只输出完整、合法的 JSON，不要使用 Markdown 代码块；必须包含 analysis 和 simple_report。"

// OpenAPIReportMessage 是第三方报告接口传入的一条完整对话消息。
type OpenAPIReportMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAPIReportSubjectProfile 是调用方显式提供的匿名受访者画像。
type OpenAPIReportSubjectProfile struct {
	Name   string `json:"name"`
	Grade  *int   `json:"grade,omitempty"`
	Class  *int   `json:"class,omitempty"`
	Gender string `json:"gender"`
}

// OpenAPIReportRequest 是私有报告生成接口的输入，不关联平台用户或会话数据。
type OpenAPIReportRequest struct {
	RequestID      string                      `json:"request_id"`
	Messages       []OpenAPIReportMessage      `json:"messages"`
	SubjectProfile OpenAPIReportSubjectProfile `json:"subject_profile"`
	MaxTokens      int                         `json:"max_tokens"`
}

// OpenAPIReportResult 只包含模型生成结果与用量，不包含任何持久化对象。
type OpenAPIReportResult struct {
	Title        string           `json:"title"`
	Digest       string           `json:"digest"`
	NeedAlarm    bool             `json:"needAlarm"`
	Analysis     *re.Analysis     `json:"analysis"`
	SimpleReport *re.SimpleReport `json:"simpleReport"`
	Usage        *OpenAPIUsage    `json:"usage,omitempty"`
}

// OpenAPIUsage 使用开放接口约定的字段名，避免暴露旧后处理消息的用量结构。
type OpenAPIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// GenerateOpenAPIReport 生成第三方报告，不读取或写入学生、会话、报告及告警数据。
// upstream 来自已经通过内部 Token 验证的 core-api；它只标识凭据，不携带明文密钥。
func GenerateOpenAPIReport(ctx context.Context, req OpenAPIReportRequest, upstream string) (*OpenAPIReportResult, error) {
	if err := ValidateOpenAPIReportRequest(req); err != nil {
		return nil, err
	}
	maxTokens, err := openAPIReportMaxTokens(req.MaxTokens)
	if err != nil {
		return nil, err
	}
	setting, err := openAPIReportSetting(upstream)
	if err != nil {
		return nil, err
	}

	msgs, err := buildOpenAPIReportMessages(ctx, req.SubjectProfile, req.Messages)
	if err != nil {
		return nil, err
	}
	cli, err := app.NewChatApp(ctx, req.RequestID, setting)
	if err != nil {
		return nil, fmt.Errorf("create report model: %w", err)
	}
	parsed, usage, err := generateOpenAPIReportWithRetry(ctx, req.RequestID, cli, msgs, maxTokens)
	if err != nil {
		return nil, err
	}
	return &OpenAPIReportResult{
		Title:        parsed.Title,
		Digest:       parsed.Digest,
		NeedAlarm:    parsed.NeedAlarm,
		Analysis:     parsed.Analysis,
		SimpleReport: parsed.SimpleReport,
		Usage:        usage,
	}, nil
}

type openAPIReportModel interface {
	Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error)
}

func generateOpenAPIReportWithRetry(ctx context.Context, requestID string, cli openAPIReportModel, msgs []*schema.Message, maxTokens int) (*re.Report, *OpenAPIUsage, error) {
	var totalUsage *OpenAPIUsage
	for attempt := 1; attempt <= maxOpenAPIReportAttempts; attempt++ {
		parsed, usage, err := generateOpenAPIReportAttempt(ctx, cli, msgs, maxTokens)
		totalUsage = addOpenAPIUsage(totalUsage, usage)
		if err == nil {
			return parsed, totalUsage, nil
		}
		if !errors.Is(err, errOpenAPIReportMalformedOutput) || attempt == maxOpenAPIReportAttempts || ctx.Err() != nil {
			return nil, nil, err
		}
		logs.CtxInfof(ctx, "[openapi report] request_id=%s status=retrying_malformed_output attempt=%d", requestID, attempt+1)
		msgs = openAPIReportRepairMessages(msgs)
	}
	return nil, nil, errOpenAPIReportMalformedOutput
}

func generateOpenAPIReportAttempt(ctx context.Context, cli openAPIReportModel, msgs []*schema.Message, maxTokens int) (*re.Report, *OpenAPIUsage, error) {
	resp, err := cli.Generate(ctx, msgs, model.WithMaxTokens(maxTokens))
	if err != nil {
		return nil, nil, fmt.Errorf("generate report: %w", err)
	}
	usage := openAPIUsage(resp)
	if resp == nil || strings.TrimSpace(resp.Content) == "" {
		return nil, usage, fmt.Errorf("%w: empty model output", errOpenAPIReportMalformedOutput)
	}
	parsed, err := extraReport(cleanJSONString(resp.Content))
	if err != nil {
		return nil, usage, fmt.Errorf("%w: parse report: %v", errOpenAPIReportMalformedOutput, err)
	}
	if parsed.Analysis == nil || parsed.SimpleReport == nil {
		return nil, usage, fmt.Errorf("%w: incomplete report structure", errOpenAPIReportMalformedOutput)
	}
	return parsed, usage, nil
}

func openAPIReportRepairMessages(msgs []*schema.Message) []*schema.Message {
	repaired := make([]*schema.Message, len(msgs))
	for i, msg := range msgs {
		if msg == nil {
			continue
		}
		copy := *msg
		if i == 0 && copy.Role == schema.System {
			copy.Content += openAPIReportRepairInstruction
		}
		repaired[i] = &copy
	}
	return repaired
}

func addOpenAPIUsage(total, current *OpenAPIUsage) *OpenAPIUsage {
	if current == nil {
		return total
	}
	if total == nil {
		copy := *current
		return &copy
	}
	total.PromptTokens += current.PromptTokens
	total.CompletionTokens += current.CompletionTokens
	total.TotalTokens += current.TotalTokens
	return total
}

func openAPIUsage(msg *schema.Message) *OpenAPIUsage {
	usage := rptUsage(msg)
	if usage == nil {
		return nil
	}
	return &OpenAPIUsage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
	}
}

// ValidateOpenAPIReportRequest 校验私有接口的输入，避免绕过开放入口直接污染模型上下文。
func ValidateOpenAPIReportRequest(req OpenAPIReportRequest) error {
	if strings.TrimSpace(req.RequestID) == "" {
		return fmt.Errorf("%w: request_id is required", ErrInvalidOpenAPIReportRequest)
	}
	if len(req.Messages) == 0 {
		return fmt.Errorf("%w: messages must not be empty", ErrInvalidOpenAPIReportRequest)
	}
	for i, msg := range req.Messages {
		if msg.Role != "user" && msg.Role != "assistant" {
			return fmt.Errorf("%w: message %d has unsupported role", ErrInvalidOpenAPIReportRequest, i)
		}
		if strings.TrimSpace(msg.Content) == "" {
			return fmt.Errorf("%w: message %d must have content", ErrInvalidOpenAPIReportRequest, i)
		}
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != "user" {
		return fmt.Errorf("%w: last message must be user", ErrInvalidOpenAPIReportRequest)
	}
	if gender := req.SubjectProfile.Gender; gender != "" && gender != "male" && gender != "female" && gender != "unknown" {
		return fmt.Errorf("%w: unsupported gender", ErrInvalidOpenAPIReportRequest)
	}
	return nil
}

func openAPIReportMaxTokens(value int) (int, error) {
	defaultValue, maxValue := defaultOpenAPIReportMaxTokens, maxOpenAPIReportMaxTokens
	if cfg := conf.GetConfig(); cfg != nil && cfg.InternalAPI != nil {
		if cfg.InternalAPI.ReportDefaultMaxTokens > 0 {
			defaultValue = cfg.InternalAPI.ReportDefaultMaxTokens
		}
		if cfg.InternalAPI.ReportMaxTokens > 0 {
			maxValue = cfg.InternalAPI.ReportMaxTokens
		}
	}
	if maxValue < 1 {
		maxValue = maxOpenAPIReportMaxTokens
	}
	if defaultValue < 1 || defaultValue > maxValue {
		defaultValue = maxValue
	}
	if value == 0 {
		return defaultValue, nil
	}
	if value < 1 || value > maxValue {
		return 0, fmt.Errorf("%w: max_tokens must be between 1 and %d", ErrInvalidOpenAPIReportRequest, maxValue)
	}
	return value, nil
}

func openAPIReportSetting(upstreamName string) (*app.ChatSetting, error) {
	upstream, err := conf.GetConfig().OpenAPIUpstreamByName(upstreamName)
	if err != nil {
		return nil, err
	}
	return &app.ChatSetting{Provider: impl.DeepSeek, Url: upstream.URL, Model: upstream.Model, AccessKey: upstream.AccessKey}, nil
}

func buildOpenAPIReportMessages(ctx context.Context, profile OpenAPIReportSubjectProfile, messages []OpenAPIReportMessage) ([]*schema.Message, error) {
	system, err := buildOpenAPIReportSystemPrompt(ctx, profile, messages)
	if err != nil {
		return nil, err
	}
	return []*schema.Message{system, schema.UserMessage(buildOpenAPIReportUserPrompt(profile, messages))}, nil
}

func buildOpenAPIReportSystemPrompt(ctx context.Context, profile OpenAPIReportSubjectProfile, messages []OpenAPIReportMessage) (*schema.Message, error) {
	if prompt.Mgr == nil {
		return nil, fmt.Errorf("report prompt manager is unavailable")
	}
	content, err := prompt.Mgr.GetOpenAPIReport(ctx)
	if err != nil {
		return nil, fmt.Errorf("load report prompt: %w", err)
	}
	if strings.TrimSpace(content) == "" {
		content, err = prompt.Mgr.GetTemplates(ctx, "post", nil)
		if err != nil {
			return nil, fmt.Errorf("load post prompt: %w", err)
		}
	}
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("report prompt is unavailable")
	}
	tpl, err := template.New("openapi_report").Parse(content)
	if err != nil {
		return nil, fmt.Errorf("parse report prompt: %w", err)
	}
	var out bytes.Buffer
	if err := tpl.Execute(&out, openAPIReportTemplateData(profile, messages)); err != nil {
		return nil, fmt.Errorf("render report prompt: %w", err)
	}
	result := strings.TrimSpace(out.String())
	if result == "" {
		return nil, fmt.Errorf("render report prompt: empty content")
	}
	return schema.SystemMessage(result + "\n\n" + riskLevelOutputInstruction), nil
}

func buildOpenAPIReportUserPrompt(profile OpenAPIReportSubjectProfile, messages []OpenAPIReportMessage) string {
	data := openAPIReportTemplateData(profile, messages)
	promptMessages := make([]reportInputMessage, 0, len(data.Messages))
	for _, msg := range data.Messages {
		promptMessages = append(promptMessages, reportInputMessage{Role: msg.Role, Content: msg.Content})
	}
	gender := "未知"
	switch data.Gender {
	case "male":
		gender = "男"
	case "female":
		gender = "女"
	}
	return buildReportInputPrompt(data.StudentName, data.Grade, data.Class, gender, promptMessages)
}

type openAPIReportTemplateMessage struct {
	Role    string
	Content string
	Index   int
}

type openAPIReportTemplatePayload struct {
	StudentName string
	Grade       string
	Class       string
	Gender      string
	Messages    []openAPIReportTemplateMessage
}

func openAPIReportTemplateData(profile OpenAPIReportSubjectProfile, messages []OpenAPIReportMessage) openAPIReportTemplatePayload {
	data := openAPIReportTemplatePayload{StudentName: "受访者", Grade: "未知", Class: "未知", Gender: "unknown"}
	if name := strings.TrimSpace(profile.Name); name != "" {
		data.StudentName = name
	}
	if profile.Grade != nil {
		data.Grade = fmt.Sprintf("%d", *profile.Grade)
	}
	if profile.Class != nil {
		data.Class = fmt.Sprintf("%d", *profile.Class)
	}
	if profile.Gender != "" {
		data.Gender = profile.Gender
	}
	data.Messages = make([]openAPIReportTemplateMessage, 0, len(messages))
	for i, msg := range messages {
		data.Messages = append(data.Messages, openAPIReportTemplateMessage{Role: msg.Role, Content: msg.Content, Index: i})
	}
	return data
}
