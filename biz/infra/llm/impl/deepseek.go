package impl

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const (
	DeepSeek                    = "deepseek"
	deepSeekChatCompletionsPath = "/v1/chat/completions"
	openAIChatCompletionsPath   = "/chat/completions"
)

type deepseekChatReq struct {
	Model          string                  `json:"model"`
	Messages       []*deepseekMessage      `json:"messages"`
	Stream         bool                    `json:"stream"`
	MaxTokens      int                     `json:"max_tokens,omitempty"`
	ResponseFormat *deepseekResponseFormat `json:"response_format,omitempty"`
}

type deepseekResponseFormat struct {
	Type string `json:"type"`
}

type deepseekOptions struct {
	JSONOutput bool
}

// WithJSONOutput 要求兼容接口返回 JSON 对象，仅适用于同步生成。
func WithJSONOutput() model.Option {
	return model.WrapImplSpecificOptFn(func(o *deepseekOptions) {
		o.JSONOutput = true
	})
}

type deepseekMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type deepseekChatResp struct {
	Choices []struct {
		Message      *deepseekMessage `json:"message,omitempty"`
		Delta        *deepseekMessage `json:"delta,omitempty"`
		FinishReason *string          `json:"finish_reason,omitempty"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type DeepSeekModel struct {
	model  string
	apiKey string
	url    string
	path   string
	// enableJSONOutput 控制是否发送 response_format=json_object。
	// 部分 OpenAI 兼容上游未声明该扩展字段，必须只依赖提示词约束 JSON。
	enableJSONOutput bool
	cli              *http.Client
}

// NewDeepSeekModel 保持既有 DeepSeek 调用路径，以兼容未配置 Provider 的旧上游。
func NewDeepSeekModel(ctx context.Context, url, apiKey, modelName, chatCompletionsPath string) (_ model.ToolCallingChatModel, err error) {
	return newChatCompletionsModel(url, apiKey, modelName, chatCompletionsPath, deepSeekChatCompletionsPath, true), nil
}

// NewOpenAICompatibleModel 用于 GLM 等采用 OpenAI Chat Completions 请求与 SSE
// 响应格式的上游。默认路径不含 /v1，避免把 GLM 的 /api/paas/v4 误拼成 /v1。
func NewOpenAICompatibleModel(ctx context.Context, url, apiKey, modelName, chatCompletionsPath string) (_ model.ToolCallingChatModel, err error) {
	return newChatCompletionsModel(url, apiKey, modelName, chatCompletionsPath, openAIChatCompletionsPath, true), nil
}

// NewInnoSparkModel 使用 InnoSpark 已声明的标准请求字段，不发送未在其文档中
// 声明的 response_format 扩展字段。
func NewInnoSparkModel(ctx context.Context, url, apiKey, modelName, chatCompletionsPath string) (_ model.ToolCallingChatModel, err error) {
	return newChatCompletionsModel(url, apiKey, modelName, chatCompletionsPath, deepSeekChatCompletionsPath, false), nil
}

func newChatCompletionsModel(url, apiKey, modelName, chatCompletionsPath, defaultPath string, enableJSONOutput bool) *DeepSeekModel {
	path := strings.TrimSpace(chatCompletionsPath)
	if path == "" {
		path = defaultPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return &DeepSeekModel{
		model:            modelName,
		apiKey:           apiKey,
		url:              strings.TrimRight(url, "/"),
		path:             path,
		enableJSONOutput: enableJSONOutput,
		cli:              http.DefaultClient,
	}
}

func (d *DeepSeekModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	msgs := e2ds(in)
	body := buildChatReq(d.model, msgs, false, opts)
	if !d.enableJSONOutput {
		body.ResponseFormat = nil
	}
	reqBytes, err := sonic.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url+d.path, bytes.NewReader(reqBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.apiKey)
	resp, err := d.cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deepseek API error %d: %s", resp.StatusCode, string(respBytes))
	}
	var chatResp deepseekChatResp
	if err = sonic.Unmarshal(respBytes, &chatResp); err != nil {
		return nil, err
	}
	msg := ds2e(&chatResp)
	if len(chatResp.Choices) > 0 && chatResp.Choices[0].FinishReason != nil {
		msg.ResponseMeta = &schema.ResponseMeta{
			FinishReason: *chatResp.Choices[0].FinishReason,
		}
	}
	if chatResp.Usage != nil {
		if msg.ResponseMeta == nil {
			msg.ResponseMeta = &schema.ResponseMeta{}
		}
		msg.ResponseMeta.Usage = &schema.TokenUsage{
			PromptTokens:     chatResp.Usage.PromptTokens,
			CompletionTokens: chatResp.Usage.CompletionTokens,
			TotalTokens:      chatResp.Usage.TotalTokens,
		}
	}
	return msg, nil
}

func (d *DeepSeekModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msgs := e2ds(in)
	body := buildChatReq(d.model, msgs, true, opts)
	reqBytes, err := sonic.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url+d.path, bytes.NewReader(reqBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.apiKey)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := d.cli.Do(req)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](5)
	go d.processStream(ctx, resp.Body, sw)
	return sr, nil
}

func buildChatReq(modelName string, messages []*deepseekMessage, stream bool, opts []model.Option) *deepseekChatReq {
	body := &deepseekChatReq{Model: modelName, Messages: messages, Stream: stream}
	common := model.GetCommonOptions(&model.Options{}, opts...)
	if common.MaxTokens != nil && *common.MaxTokens > 0 {
		body.MaxTokens = *common.MaxTokens
	}
	if !stream && model.GetImplSpecificOptions(&deepseekOptions{}, opts...).JSONOutput {
		body.ResponseFormat = &deepseekResponseFormat{Type: "json_object"}
	}
	return body
}

func (d *DeepSeekModel) processStream(ctx context.Context, body io.ReadCloser, sw *schema.StreamWriter[*schema.Message]) {
	defer body.Close()
	defer sw.Close()
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return
		}
		var chunk deepseekChatResp
		if err := sonic.Unmarshal([]byte(data), &chunk); err != nil {
			sw.Send(nil, err)
			return
		}
		msg := ds2e(&chunk)
		if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason != nil {
			msg.ResponseMeta = &schema.ResponseMeta{
				FinishReason: *chunk.Choices[0].FinishReason,
			}
		}
		if chunk.Usage != nil {
			if msg.ResponseMeta == nil {
				msg.ResponseMeta = &schema.ResponseMeta{}
			}
			msg.ResponseMeta.Usage = &schema.TokenUsage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
			}
		}
		sw.Send(msg, nil)
	}
	if err := scanner.Err(); err != nil {
		sw.Send(nil, err)
	}
}

func (d *DeepSeekModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return d, nil
}

func e2ds(in []*schema.Message) (out []*deepseekMessage) {
	for _, i := range in {
		out = append(out, &deepseekMessage{
			Role:    string(i.Role),
			Content: i.Content,
		})
	}
	return
}

func ds2e(resp *deepseekChatResp) *schema.Message {
	msg := &schema.Message{Role: schema.Assistant}
	if len(resp.Choices) == 0 {
		return msg
	}
	if resp.Choices[0].Message != nil {
		msg.Content = resp.Choices[0].Message.Content
		msg.ReasoningContent = resp.Choices[0].Message.Content
	} else if resp.Choices[0].Delta != nil {
		msg.Content = resp.Choices[0].Delta.Content
	}
	return msg
}
