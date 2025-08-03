package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/bytedance/gopkg/util/gopool"
	amqp "github.com/rabbitmq/amqp091-go"
	logx "github.com/xh-polaris/gopkg/util/log"
	"github.com/xh-polaris/psych-pkg/app"
	"github.com/xh-polaris/psych-pkg/core"
	"github.com/xh-polaris/psych-post/biz/infra/config"
	"github.com/xh-polaris/psych-post/biz/infra/consts"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/history"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/report"
	"github.com/xh-polaris/psych-post/biz/infra/redis"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"log"
	"strings"
	"time"
)

// HisConsumer 聊天记录后处理消费者
type HisConsumer struct {
	me      int
	rs      *core.HisRedis
	channel *amqp.Channel
	ctx     context.Context
}

// NewHisConsumer 创建一个消费者
func NewHisConsumer(ctx context.Context, i int) *HisConsumer {
	var ch *amqp.Channel
	var err error

	if ch, err = getConn().Channel(); err != nil {
		log.Panicf("[mq consumer %d] get channel err:%v", i, err)
	}
	return &HisConsumer{ctx: ctx, channel: ch, rs: core.GetHisRedis(redis.NewRedis(config.GetConfig()))}
}

// Consume 启动消费goroutine
func (c *HisConsumer) Consume() {
	gopool.CtxGo(c.ctx, func() { c.consume(c.ctx) })
}

// consume 获取消息并执行处理
func (c *HisConsumer) consume(ctx context.Context) {
	var err error
	var msg <-chan amqp.Delivery

	defer func() { _ = c.channel.Close() }()
	if err = c.channel.Qos(1, 0, false); err != nil {
		log.Panicf("[mq consumer %d] set qos err:%v", c.me, err)
	}
	if msg, err = c.channel.Consume("psych_his", fmt.Sprintf("psych_his_consumer_%d", c.me), false, false, false, false, nil); err != nil {
		log.Panicf("[mq consumer %d] get consume err:%v", c.me, err)
	}

	for delivery := range msg {
		if err = c.process(&delivery); err != nil {
			if err = delivery.Nack(false, true); err != nil {
				logx.Error("[mq consumer %d] set nack err:%v", c.me, err)
			}
		} else if err = delivery.Ack(false); err != nil {
			logx.Error("[mq consumer %d] ack err:%v", c.me, err)
		}
	}
}

// process 实际处理
func (c *HisConsumer) process(delivery *amqp.Delivery) (err error) {
	var his *history.History
	var notify *core.PostNotify

	if err = json.Unmarshal(delivery.Body, &notify); err != nil {
		logx.Error("[mq consumer %d] process unmarshal err:%v", c.me, err)
		return err
	}
	if his, err = c.history(notify); err != nil { // 处理聊天记录
		logx.Error("[mq consumer %d] process history err:%v", c.me, err)
		return err
	}
	if err = c.report(notify, his); err != nil { // 处理对话报表
		logx.Error("[mq consumer %d] process report err:%v", c.me, err)
		return err
	}
	return
}

// history 处理历史记录
func (c *HisConsumer) history(notify *core.PostNotify) (his *history.History, err error) {
	// 判断是否处理过历史记录
	mapper := history.GetMongoMapper()
	if his, err = mapper.FindOneBySession(c.ctx, notify.Session); err == nil && his != nil { // 处理过, 不再重复处理
		return his, nil
	} else if !errors.Is(err, consts.NotFound) { // 其余错误, 处理失败
		return nil, err
	}

	// 获取基础信息
	var verify bool
	var unitID, userID primitive.ObjectID
	if unitID, err = primitive.ObjectIDFromHex(notify.Info["unit_id"].(string)); err != nil {
		return nil, err
	}
	delete(notify.Info, "unit_id")
	if userID, err = primitive.ObjectIDFromHex(notify.Info["user_id"].(string)); err != nil {
		return nil, err
	}
	delete(notify.Info, "user_id")
	verify = notify.Info["verify"].(bool)
	delete(notify.Info, "verify")

	// 获取对话记录
	var entries []*core.HisEntry
	if entries, err = c.rs.Load(notify.Session); err != nil {
		return nil, err
	}

	// 插入历史记录
	his = &history.History{
		Session:  notify.Session,
		UnitID:   unitID,
		UserID:   userID,
		Verify:   verify,
		Info:     notify.Info,
		Config:   notify.Config,
		HisEntry: entries,
		Start:    time.Unix(notify.Start, 0),
		End:      time.Unix(notify.End, 0),
	}
	return his, mapper.Insert(c.ctx, his)
}

// 处理对话报表
func (c *HisConsumer) report(notify *core.PostNotify, his *history.History) (err error) {
	var ra app.ReportApp
	var resp *app.Report
	var reportSetting *app.ReportSetting

	// TODO 获取report 配置
	//pm := rpc.GetPsychModel()

	// 构建report app
	if ra, err = app.NewReportApp(notify.Session, reportSetting); err != nil {
		return err
	}

	// 生成report
	if resp, err = ra.Call(c.ctx, prompt(his)); err != nil {
		return err
	}
	// 构造report并插入
	return report.GetMongoMapper().Insert(c.ctx, report.ConvReport(his, resp))
}

// 构建对话记录
func prompt(his *history.History) string {
	var sb strings.Builder
	for _, entry := range his.HisEntry {
		sb.WriteString(entry.Role)
		sb.WriteString(":")
		sb.WriteString(entry.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}
