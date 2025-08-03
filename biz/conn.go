package biz

import (
	"github.com/avast/retry-go"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/xh-polaris/psych-pkg/util/logx"
	"github.com/xh-polaris/psych-post/biz/infra/config"
	"sync"
	"time"
)

// conn 采用单例模式, 复用连接
var (
	conn     *amqp.Connection
	once     sync.Once
	url      string
	maxRetry = 5
)

// getConn 获取连接单例
func getConn() *amqp.Connection {
	once.Do(func() {
		var err error
		url = config.GetConfig().RabbitMQ.Url
		if conn, err = amqp.Dial(url); err != nil {
			panic("[mq] connect failed:" + err.Error())
		}
		go monitor() // 自动重连监听
	})
	return conn
}

// monitor 监听健康状态并重连
func monitor() {
	opts := []retry.Option{
		retry.Attempts(uint(maxRetry)),      // 最大重试次数
		retry.DelayType(retry.BackOffDelay), // 指数退避策略
		retry.MaxDelay(64 * time.Second),    // 最大退避间隔
		retry.OnRetry(func(n uint, err error) { // 重试日志
			logx.Info("[mq consumer] retry #%d times with err:%v", n+1, err)
		}),
	}

	operation := func() (err error) {
		if conn, err = amqp.Dial(url); err == nil {
			logx.Info("[mq consumer] reconnect")
		}
		return err
	}

	for {
		reason := <-conn.NotifyClose(make(chan *amqp.Error))
		logx.Info("[mq consumer] connection closed , reason: ", reason)
		if err := retry.Do(operation, opts...); err != nil {
			panic("[mq consumer] retry too many times:" + err.Error())
		}
		conn.NotifyClose(make(chan *amqp.Error)) // 重新监听
	}
}
