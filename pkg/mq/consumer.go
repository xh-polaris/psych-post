package mq

import (
	"context"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/xh-polaris/psych-post/pkg/logs"
)

type Consumer struct {
	connMgr *ConnManager                                        // 消息队列连接管理
	channel *amqp.Channel                                       // 消息队列的channel
	close   chan struct{}                                       // 关闭通知
	f       func(context.Context, *amqp.Delivery) (bool, error) // 消息处理函数
	wg      *sync.WaitGroup
}

type ConsumeConfig struct {
	PrefetchCount int
	PrefetchSize  int
	Global        bool
	Queue         string
	Consumer      string
	AutoAck       bool
	Exclusive     bool
	NoLocal       bool
	NoWait        bool
	Args          amqp.Table
	NackMultiple  bool
	NackRequeue   bool
	AckMultiple   bool
	MQErrInterval time.Duration
}

func NewConsumer(mgr *ConnManager, f func(context.Context, *amqp.Delivery) (bool, error), wg *sync.WaitGroup) *Consumer {
	return &Consumer{connMgr: mgr, f: f, close: make(chan struct{}, 1), wg: wg}
}

// Consume 不自动ACK, 每次消费一个消息, 无限重试直到关闭
func (c *Consumer) Consume(conf *ConsumeConfig) {
	defer c.wg.Done()
	for {
		ctx, cancel := context.WithCancel(context.Background())
		select {
		case <-c.close: // 结束消费
			cancel()
			return
		default:
			if err := c.consume(ctx, conf); err != nil {
				logs.Error(err)
			}
			cancel()
			// delivery关闭, 对应着channel关闭
			time.Sleep(conf.MQErrInterval)
		}
	}
}

func (c *Consumer) consume(ctx context.Context, conf *ConsumeConfig) (err error) {
	var (
		ch       *amqp.Channel
		delivery <-chan amqp.Delivery
	)

	// 获取channel
	if ch, err = c.connMgr.Channel(); err != nil {
		return
	}
	defer func() { _ = ch.Close() }()
	chClose := ch.NotifyClose(make(chan *amqp.Error, 1))

	// 设置消费参数
	if err = ch.Qos(conf.PrefetchCount, conf.PrefetchSize, conf.Global); err != nil {
		return
	}

	// 获取delivery
	if delivery, err = ch.Consume(conf.Queue, conf.Consumer, conf.AutoAck,
		conf.Exclusive, conf.NoLocal, conf.NoWait, conf.Args); err != nil {
		return
	}
	for {
		select {
		case d, ok := <-delivery:
			if !ok {
				return
			}
			if ok, err = c.f(ctx, &d); err != nil || !ok {
				// 处理失败, 重新入队
				if err = d.Nack(conf.NackMultiple, conf.NackRequeue); err != nil {
					return
				}
			} else if err = d.Ack(conf.AckMultiple); err != nil {
				return
			}
		case <-c.close:
			return
		case <-chClose:
			return
		}
	}
}

func (c *Consumer) Close() {
	close(c.close)
}
