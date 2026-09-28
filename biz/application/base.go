package application

import (
	"github.com/xh-polaris/psych-post/biz/conf"
	"github.com/xh-polaris/psych-post/biz/domain/his"
	"github.com/xh-polaris/psych-post/biz/domain/prompt"
	"github.com/xh-polaris/psych-post/biz/infra/cache"
	"github.com/xh-polaris/psych-post/biz/infra/cache/redis"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/alarm"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/conversation"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/message"
	promptmapper "github.com/xh-polaris/psych-post/biz/infra/mapper/prompt"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/report"
	"github.com/xh-polaris/psych-post/pkg/mq"
)

type AppDependency struct {
	Cache              cache.Cmdable
	AlarmMapper        alarm.IMongoMapper
	MessageMapper      message.IMongoMapper
	ConversationMapper conversation.IMongoMapper
	ReportMapper       report.IMongoMapper
	HisMgr             *his.HistoryManager
	ConnManager        *mq.ConnManager
}

func InitAppDependency() {
	deps := &AppDependency{}
	deps.Cache = redis.New()
	deps.AlarmMapper = alarm.NewAlarmMongoMapper(conf.GetConfig())
	deps.MessageMapper = message.NewMessageMongoMapper(conf.GetConfig())
	deps.ConversationMapper = conversation.NewConversationMongoMapper(conf.GetConfig())
	deps.ReportMapper = report.NewConfigMongoMapper(conf.GetConfig())
	his.New(deps.Cache, deps.MessageMapper, deps.ConversationMapper)
	deps.HisMgr = his.Mgr
	prompt.New(deps.Cache, promptmapper.NewPromptMongoMapper(conf.GetConfig()))
	deps.ConnManager = mq.NewConnManager(conf.GetConfig().RabbitMQ.Url)
}

func Init() {
	InitAppDependency()
}
