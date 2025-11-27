package mq

import (
	"sync"
	"time"

	"github.com/avast/retry-go"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/xh-polaris/psych-post/pkg/logs"
)

var (
	maxRetry = 5
	opts     = []retry.Option{
		retry.Attempts(uint(maxRetry)),      // 最大重试次数
		retry.DelayType(retry.BackOffDelay), // 指数退避策略
		retry.MaxDelay(64 * time.Second),    // 最大退避间隔
		retry.OnRetry(func(n uint, err error) { // 重试日志
			logs.Info("[mq consumer] retry #%d times with err:%v", n+1, err)
		}),
	}
)

type ConnManager struct {
	mu   sync.RWMutex
	conn *amqp.Connection
	url  string
}

func NewConnManager(url string) *ConnManager {
	cm := &ConnManager{url: url}

	if err := cm.connect(); err != nil {
		panic("[mq consumer] connect err:" + err.Error()) // 一开始就无法建立连接直接panic
	}
	go cm.monitor()
	return cm
}

func (cm *ConnManager) connect() (err error) {
	operation := func() (err error) {
		cm.mu.Lock()
		defer cm.mu.Unlock()
		if cm.conn, err = amqp.Dial(cm.url); err == nil {
			logs.Info("[mq consumer] reconnect")
		}
		return err
	}
	if err = retry.Do(operation, opts...); err != nil {
		return
	}
	return
}

// GetConn 获取连接单例
func (cm *ConnManager) GetConn() *amqp.Connection {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.conn
}

// monitor 监听健康状态并重连
func (cm *ConnManager) monitor() {
	operation := func() (err error) {
		cm.mu.Lock()
		defer cm.mu.Unlock()
		if cm.conn, err = amqp.Dial(cm.url); err == nil {
			logs.Info("[mq consumer] reconnect")
		}
		return err
	}

	for {
		reason := <-cm.conn.NotifyClose(make(chan *amqp.Error))
		logs.Info("[mq consumer] connection closed , reason: ", reason)
		if err := retry.Do(operation, opts...); err != nil {
			logs.Errorf("[mq consumer] retry too many times: %s", err)
			time.Sleep(180 * time.Second) // 三分钟后重试
		}
		cm.conn.NotifyClose(make(chan *amqp.Error)) // 重新监听
	}
}

func (cm *ConnManager) Channel() (c *amqp.Channel, err error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	if cm.conn == nil {
		return
	}
	return cm.conn.Channel()
}
