package application

import (
	"github.com/xh-polaris/psych-post/biz/conf"
	"github.com/xh-polaris/psych-post/biz/domain/his"
	"github.com/xh-polaris/psych-post/biz/infra/cache"
	"github.com/xh-polaris/psych-post/biz/infra/cache/redis"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/message"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/report"
	"github.com/xh-polaris/psych-post/pkg/mq"
)

type AppDependency struct {
	Cache         cache.Cmdable
	MessageMapper message.MongoMapper
	ReportMapper  report.IMongoMapper
	HisMgr        *his.HistoryManager
	ConnManager   *mq.ConnManager
}

func InitAppDependency() {
	deps := &AppDependency{}
	deps.Cache = redis.New()
	deps.MessageMapper = message.NewMessageMongoMapper(conf.GetConfig())
	deps.ReportMapper = report.New(conf.GetConfig())
	his.New(deps.Cache, deps.MessageMapper)
	deps.HisMgr = his.Mgr
	deps.ConnManager = mq.NewConnManager(conf.GetConfig().RabbitMQ.Url)
}

func Init() {
	InitAppDependency()
}
