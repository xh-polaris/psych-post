package impl

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/coze-dev/coze-go"
)

const (
	Coze = "coze"
)

var (
	autoSaveHistory = true
	noSaveHistory   = false
	isStream        = true
	noStream        = false
)

type CozeModel struct {
	model string
	cli   *coze.CozeAPI
	uid   string
	botId string
}

func NewCozeModel(ctx context.Context, url, sk, uid, botId string) (_ model.ToolCallingChatModel, err error) {
	cozeCli := coze.NewCozeAPI(coze.NewTokenAuth(sk), coze.WithBaseURL(url))
	return &CozeModel{Coze, &cozeCli, uid, botId}, nil
}

func (c *CozeModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	cmsg, err := c.cli.Chat.CreateAndPoll(ctx, &coze.CreateChatsReq{
		BotID:           c.botId,
		UserID:          c.uid,
		Messages:        e2c(in),
		Stream:          &noStream,
		AutoSaveHistory: &noSaveHistory,
		ConnectorID:     "1024",
	}, nil)
	if err != nil {
		return nil, err
	}
	e := c2e(cmsg.Messages[0])
	// 记录用量
	e.ResponseMeta = &schema.ResponseMeta{
		FinishReason: cmsg.Chat.LastError.Msg,
		Usage: &schema.TokenUsage{
			PromptTokens:     cmsg.Chat.Usage.InputCount,
			CompletionTokens: cmsg.Chat.Usage.OutputCount,
			TotalTokens:      cmsg.Chat.Usage.TokenCount,
		},
		LogProbs: nil,
	}
	return e, nil
}

func (c *CozeModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (sr *schema.StreamReader[*schema.Message], err error) {
	sr, sw := schema.Pipe[*schema.Message](5)
	request := &coze.CreateChatsReq{
		BotID:           c.botId,
		UserID:          c.uid,
		Messages:        e2c(in),
		AutoSaveHistory: &noSaveHistory,
		Stream:          &isStream,
		ConnectorID:     "1024",
	}
	var stream coze.Stream[coze.ChatEvent]
	if stream, err = c.cli.Chat.Stream(ctx, request); err != nil {
		return nil, err
	}
	go process(ctx, stream, sw)
	return sr, nil
}

func process(ctx context.Context, reader coze.Stream[coze.ChatEvent], writer *schema.StreamWriter[*schema.Message]) {
	defer func() { _ = reader.Close() }()
	defer writer.Close()

	var err error
	var event *coze.ChatEvent
	var msg *schema.Message

	for {
		select {
		case <-ctx.Done():
			return
		default:
			if event, err = reader.Recv(); err != nil {
				writer.Send(nil, err)
				return
			}
			if event.Message == nil || event.Event != coze.ChatEventConversationMessageDelta {
				if event.Message != nil && event.Message.Type == coze.MessageTypeFollowUp {
					msg = ce2e(event)
					writer.Send(msg, nil)
				}
				continue
			}
			msg = ce2e(event)
			writer.Send(msg, nil)
		}
	}
}

func (c *CozeModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return c, nil
}

// eino消息转coze消息
func e2c(in []*schema.Message) (c []*coze.Message) {
	for _, i := range in {
		m := &coze.Message{
			Role:             coze.MessageRole(i.Role),
			Content:          i.Content,
			ReasoningContent: i.ReasoningContent,
			Type:             "question",
			ContentType:      "text",
		}
		c = append(c, m)
	}
	return
}

func ce2e(e *coze.ChatEvent) *schema.Message {
	return c2e(e.Message)
}

// coze消息转eino消息
func c2e(c *coze.Message) *schema.Message {
	return &schema.Message{
		Role:    schema.Assistant,
		Content: c.Content,
	}
}
