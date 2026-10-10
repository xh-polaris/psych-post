package llm

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/xh-polaris/psych-post/biz/infra/llm/impl"
	"github.com/xh-polaris/psych-post/pkg/app"
	"github.com/xh-polaris/psych-post/pkg/errorx"
	"github.com/xh-polaris/psych-post/type/errno"
)

const (
	ProviderCoze             = "coze"
	ProviderDeepSeek         = "deepseek"
	ProviderGLM              = "glm"
	ProviderInnoSpark        = "innospark"
	ProviderOpenAICompatible = "openai-compatible"
)

func init() {
	app.ChatRegister(ProviderCoze, NewChatModel)
	app.ChatRegister(ProviderDeepSeek, NewChatModel)
	app.ChatRegister(ProviderGLM, NewChatModel)
	app.ChatRegister(ProviderInnoSpark, NewChatModel)
	app.ChatRegister(ProviderOpenAICompatible, NewChatModel)
}

// ChatModel 对话大模型
type ChatModel struct {
	cli                                   model.ToolCallingChatModel
	uSession, provider, model, botId, uid string
}

// NewChatModel 根据provider创建对应的对话大模型
func NewChatModel(ctx context.Context, uSession string, s *app.ChatSetting) (_ model.ToolCallingChatModel, err error) {
	cm := &ChatModel{uSession: uSession, provider: s.Provider, model: s.Model, botId: s.BotId, uid: s.UserId}
	if cm.cli, err = newCli(ctx, s.Provider, s.Url, s.AccessKey, s.Model, s.BotId, s.UserId, s.ChatCompletionsPath); err != nil {
		return
	}
	return cm, nil
}

func newCli(ctx context.Context, provider, url, sk, model, botId, uid, chatCompletionsPath string) (_ model.ToolCallingChatModel, err error) {
	switch provider {
	case impl.Coze:
		return impl.NewCozeModel(ctx, url, sk, uid, botId)
	case impl.DeepSeek:
		return impl.NewDeepSeekModel(ctx, url, sk, model, chatCompletionsPath)
	case ProviderGLM, ProviderOpenAICompatible:
		return impl.NewOpenAICompatibleModel(ctx, url, sk, model, chatCompletionsPath)
	case ProviderInnoSpark:
		return impl.NewInnoSparkModel(ctx, url, sk, model, chatCompletionsPath)
	default:
		return nil, errorx.New(errno.UnImplementErr)
	}
}

func (m *ChatModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (_ *schema.Message, err error) {
	return m.cli.Generate(ctx, in, opts...)
}

func (m *ChatModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (_ *schema.StreamReader[*schema.Message], err error) {
	return m.cli.Stream(ctx, in, opts...)
}

func (m *ChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
