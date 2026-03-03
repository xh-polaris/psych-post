package report

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/xh-polaris/psych-idl/kitex_gen/profile"
	"github.com/xh-polaris/psych-post/biz/conf"
	"github.com/xh-polaris/psych-post/biz/domain/his"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/message"
	re "github.com/xh-polaris/psych-post/biz/infra/mapper/report"
	"github.com/xh-polaris/psych-post/biz/infra/rpc"
	"github.com/xh-polaris/psych-post/biz/infra/util"
	"github.com/xh-polaris/psych-post/pkg/app"
	"github.com/xh-polaris/psych-post/pkg/core"
	"github.com/xh-polaris/psych-post/pkg/logs"
	"github.com/xh-polaris/psych-post/pkg/mq"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// ConsumeManager 管理报表生成任务的消息消费
type ConsumeManager struct {
	ConnMgr        *mq.ConnManager // 连接管理
	Consumers      []*mq.Consumer  // 消费者
	ConsumersCount int             // 消费者数量
	wg             *sync.WaitGroup
}

func New(consumer int) *ConsumeManager {
	cm := &ConsumeManager{ConnMgr: mq.NewConnManager(conf.GetConfig().RabbitMQ.Url), ConsumersCount: consumer, wg: &sync.WaitGroup{}}
	return cm
}

func (cm *ConsumeManager) BuildConsumer() *ConsumeManager {
	for range cm.Consumers {
		cm.Consumers = append(cm.Consumers, mq.NewConsumer(cm.ConnMgr, cm.DoConsume, cm.wg))
	}
	return cm
}

func (cm *ConsumeManager) StartConsume() {
	for i, consumer := range cm.Consumers {
		cfg := &mq.ConsumeConfig{
			PrefetchCount: 1,
			PrefetchSize:  0,
			Global:        false,
			Queue:         "psych.his",
			Consumer:      fmt.Sprintf("psych.his-%d", i),
			AutoAck:       false,
			Exclusive:     false,
			NoLocal:       false,
			NoWait:        false,
			Args:          nil,
			NackMultiple:  false,
			NackRequeue:   false,
			AckMultiple:   false,
			MQErrInterval: time.Second * 10,
		}
		consumer.Consume(cfg)
		cm.wg.Add(1)
	}
}

func (cm *ConsumeManager) Close() {
	for _, consumer := range cm.Consumers {
		go consumer.Close()
	}
	cm.wg.Wait()
	return
}

func (cm *ConsumeManager) DoConsume(ctx context.Context, d *amqp.Delivery) (ok bool, err error) {
	var notify *core.PostNotify
	session := bson.NewObjectID().Hex()

	// 解析消息
	if err = sonic.Unmarshal(d.Body, &notify); err != nil {
		logs.Errorf("[mq consumer] unmarshal err: %s", err)
		return
	}
	// 获取配置
	cfg, err := rpc.GetPsychProfile().ConfigGetByUnitID(ctx, &profile.ConfigGetByUnitIdReq{UnitId: notify.UnitId, Admin: true})
	if err != nil {
		logs.Errorf("[mq consumer] get unit config err: %s", err)
		return
	}
	// 构造完整配置
	reportSetting, err := conf.GetConfig().ReportConf(cfg.GetConfig().GetReport())
	if err != nil {
		logs.Errorf("[mq consumer] build report config err: %s", err)
		return
	}
	// 创建报表处理智能体
	cli, err := app.NewChatApp(ctx, session, reportSetting)
	if err != nil {
		logs.Errorf("[mq consumer] build report app err: %s", err)
		return
	}
	// 获取聊天记录
	msgs, err := his.Mgr.RetrieveMessage(ctx, notify.Session, -1)
	if err != nil {
		logs.Errorf("[mq consumer] retrieve message err: %s", err)
		return
	}
	reverse(msgs) // 按时间顺序正序
	// 构造报表生成提示词
	prompt, count, err := buildPrompt(notify, msgs)
	if err != nil {
		logs.Errorf("[mq consumer] build prompt err: %s", err)
		return
	}
	// 产生报表
	resp, err := cli.Generate(ctx, prompt)
	if err != nil {
		logs.Errorf("[mq consumer] generate err: %s", err)
		return
	}
	// 解析报表
	clean := strings.Trim(resp.Content, " `\r\n\t")
	var result re.Result
	if err = sonic.Unmarshal([]byte(clean), &result); err != nil {
		logs.Errorf("[mq consumer] unmarshal err: %s", err)
		return
	}
	oids, err := util.ObjectIDsFromHex(notify.UnitId, notify.UserId, notify.Session)
	if err != nil {
		return
	}
	// 存储报表
	report := &re.Report{
		ID:          bson.NewObjectID(),
		UnitID:      oids[0],
		UserID:      oids[1],
		Session:     oids[2],
		ReportUsage: usage(resp),
		ChatUsage:   notify.Usage.LLMUsage,
		TTSUsage:    notify.Usage.TTSUsage,
		ASRUsage:    notify.Usage.ASRUsage,
		Round:       count,
		Start:       time.Unix(notify.Start, 0),
		End:         time.Unix(notify.End, 0),
		Config:      notify.Config,
		Info:        notify.Info,
		Result:      &result,
		Keywords:    result.GetKeywords(),
	}
	if err = re.Mapper.InsertOne(ctx, report); err != nil {
		logs.Error("[mq consumer] insert report err:", err)
		return
	}
	return true, nil
}

func buildPrompt(notify *core.PostNotify, msgs []*message.Message) ([]*schema.Message, int, error) {
	var count int
	var sb strings.Builder
	infoStr, err := sonic.Marshal(notify.Info)
	if err != nil {
		return nil, 0, err
	}
	sb.WriteString("额外信息:")
	sb.WriteString(string(infoStr))
	sb.WriteString("\n")
	for _, m := range msgs {
		if m.Content != "" { // 消息有效
			count++
			sb.WriteString("<|")
			sb.WriteString(message.RoleItoS[m.Role])
			sb.WriteString("|>")
			sb.WriteString(" ")
			sb.WriteString(m.Content)
			sb.WriteString("\n")
		}
	}
	return []*schema.Message{schema.UserMessage(sb.String())}, count, nil
}

func usage(msg *schema.Message) *core.LLMUsage {
	return &core.LLMUsage{
		PromptTokens: msg.ResponseMeta.Usage.PromptTokens,
		PromptTokenDetails: core.PromptTokenDetails{
			CachedTokens: msg.ResponseMeta.Usage.PromptTokenDetails.CachedTokens,
		},
		CompletionTokens: msg.ResponseMeta.Usage.CompletionTokens,
		TotalTokens:      msg.ResponseMeta.Usage.TotalTokens,
	}
}

func reverse(msgs []*message.Message) {
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
}
