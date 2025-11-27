package conf

import (
	"github.com/xh-polaris/psych-idl/kitex_gen/profile"
	"github.com/xh-polaris/psych-post/pkg/app"
	"github.com/xh-polaris/psych-post/pkg/errorx"
	"github.com/xh-polaris/psych-post/type/errno"
)

type ModelConfig struct {
	Chat map[string]*ChatConfig
}

type ChatConfig struct {
	URL       string
	AccessKey string
}

// ReportConf 获取对话配置
func (c *Config) ReportConf(chat *profile.ReportApp) (*app.ChatSetting, error) {
	if chat == nil {
		return nil, errorx.New(errno.ConfigErr, errorx.KV("app", "chat"))
	}
	if cc, ok := c.ModelConfig.Chat[chat.Provider]; ok {
		return &app.ChatSetting{Provider: chat.Provider, Url: cc.URL, Model: "",
			BotId: chat.AppId, UserId: "", AccessKey: cc.AccessKey}, nil
	}
	return nil, errorx.New(errno.ConfigErr, errorx.KV("app", "chat"))
}
