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
	"github.com/xh-polaris/psych-post/biz/conf"
	"github.com/xh-polaris/psych-post/biz/domain/his"
	_ "github.com/xh-polaris/psych-post/biz/infra/llm"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/alarm"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/config"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/message"
	re "github.com/xh-polaris/psych-post/biz/infra/mapper/report"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/user"
	"github.com/xh-polaris/psych-post/biz/infra/util"
	"github.com/xh-polaris/psych-post/pkg/app"
	"github.com/xh-polaris/psych-post/pkg/core"
	"github.com/xh-polaris/psych-post/pkg/errorx"
	"github.com/xh-polaris/psych-post/pkg/logs"
	"github.com/xh-polaris/psych-post/pkg/mq"
	"github.com/xh-polaris/psych-post/type/enum"
	"github.com/xh-polaris/psych-post/type/errno"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// ConsumeManager 管理报表生成任务的消息消费
type ConsumeManager struct {
	ConnMgr        *mq.ConnManager // 连接管理
	Consumers      []*mq.Consumer  // 消费者
	ConsumersCount int             // 消费者数量

	ConfigMapper config.IMongoMapper
	UserMapper   user.IMongoMapper
	wg           *sync.WaitGroup
}

func New(consumer int, cfgMapper config.IMongoMapper, usrMapper user.IMongoMapper) *ConsumeManager {
	cm := &ConsumeManager{ConnMgr: mq.NewConnManager(conf.GetConfig().RabbitMQ.Url), ConsumersCount: consumer, wg: &sync.WaitGroup{}, ConfigMapper: cfgMapper, UserMapper: usrMapper}
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
		go consumer.Consume(cfg)
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
	// 解析消息
	notify := &core.PostNotify{}
	if err = sonic.Unmarshal(d.Body, notify); err != nil {
		logs.Errorf("[mq consumer] unmarshal err: %s", err)
		return
	}
	// 获取配置
	unitOID, err := util.ObjectIDsFromHex(notify.UnitId)
	if err != nil {
		logs.Errorf("[mq consumer] invalid unitID from notify")
		return false, err
	}
	cfg, err := cm.ConfigMapper.FindOneByUnitID(ctx, unitOID[0])
	if err != nil {
		logs.Errorf("[mq consumer] get unit config err: %s", err)
		return
	}
	// 构造完整配置
	reportSetting, err := buildReportSetting(conf.GetConfig(), cfg.Report, notify.UserId)
	if err != nil {
		logs.Errorf("[mq consumer] build report config err: %s", err)
		return
	}
	// 创建报表处理智能体（实际采用ChatApp，传入ReportApp的AppID）
	cli, err := app.NewChatApp(ctx, notify.Session, reportSetting)
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
	prompt, count, err := cm.buildPrompt(ctx, notify, msgs)
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
	// 解析并填充报表内容
	clean := cleanJSONString(resp.Content)

	result, err := extraReport(clean)
	if err != nil {
		logs.Errorf("[mq consumer] unmarshal err: %s, content: %s", err, clean)
		return
	}
	// 补充报表meta并存入数据库
	oids, err := util.ObjectIDsFromHex(notify.UnitId, notify.UserId, notify.Session)
	if err != nil {
		return
	}

	report := &re.Report{
		ID:             bson.NewObjectID(),
		UnitID:         oids[0],
		UserID:         oids[1],
		ConversationID: oids[2],
		ReportUsage:    rptUsage(resp),
		ASRUsage:       notify.Usage.ASRUsage,
		TTSUsage:       notify.Usage.TTSUsage,
		Round:          count,
		Start:          time.Unix(notify.Start, 0),
		End:            time.Unix(notify.End, 0),
		Config:         cfg.Report,
		Info:           notify.Info,
		Title:          result.Title,
		Keywords:       result.Keywords,
		Digest:         result.Digest,
		Emotion:        result.Emotion,
		Body:           result.Body,
		NeedAlarm:      result.NeedAlarm,
	}
	if err = re.Mapper.InsertOne(ctx, report); err != nil {
		logs.Error("[mq consumer] insert report err:", err)
		return
	}
	// 检查是否需要创建预警
	if result.NeedAlarm {
		al := alarm.Alarm{
			ID:             bson.NewObjectID(),
			UnitID:         oids[0],
			UserID:         oids[1],
			ConversationID: oids[2],
			Emotion:        result.Emotion,
			Keywords:       result.Keywords,
			Status:         enum.AlarmStatusPending,
			CreateTime:     time.Now(),
		}
		if err = alarm.Mapper.Insert(ctx, &al); err != nil {
			logs.Error("[mq consumer] insert alarm err:", err)
			return
		}
	}

	return true, nil
}

func buildReportSetting(c *conf.Config, rptConf *config.Report, uid string) (*app.ChatSetting, error) {
	if rptConf == nil {
		return nil, errorx.New(errno.ConfigErr, errorx.KV("app", "chat"))
	}
	// 传入ReportApp的AppID
	if cc, ok := c.ModelConfig.Chat[rptConf.Provider]; ok {
		return &app.ChatSetting{
			Provider:  rptConf.Provider,
			Url:       cc.URL,
			BotId:     rptConf.AppID,
			UserId:    uid,
			AccessKey: cc.AccessKey,
		}, nil
	}
	return nil, errorx.New(errno.ConfigErr, errorx.KV("app", "chat"))
}

func (cm *ConsumeManager) buildPrompt(ctx context.Context, notify *core.PostNotify, msgs []*message.Message) ([]*schema.Message, int, error) {
	var count int
	var sb strings.Builder

	// 填充学生信息
	oid, err := util.ObjectIDsFromHex(notify.UnitId, notify.UserId)
	if err != nil {
		logs.Errorf("[mq consumer] invalid userId: %s", err)
		return nil, 0, err
	}
	usr, err := cm.UserMapper.FindOneById(ctx, oid[0])
	if err != nil {
		logs.Errorf("[mq consumer] get user err: %s", err)
		return nil, 0, err
	}

	infoStr := fmt.Sprintf("学生基本信息:\n学生姓名：%s，%d年级%d班，性别%s。\n", usr.Name, usr.Grade, usr.Class, enum.GenderI2S[usr.Gender])
	sb.WriteString(infoStr)
	sb.WriteString("对话内容：\n")
	for _, m := range msgs {
		if m.Content != "" { // 消息有效
			count++
			sb.WriteString("<|")
			sb.WriteString(enum.MsgRoleItoA[m.Role])
			sb.WriteString("|>")
			sb.WriteString(" ")
			sb.WriteString(m.Content)
			sb.WriteString("\n")
		}
	}
	return []*schema.Message{schema.UserMessage(sb.String())}, count, nil
}

func cleanJSONString(input string) string {
	// 以```json\n 开头
	if strings.HasPrefix(input, "```json") {
		// 找到第一个换行符的位置
		firstNewline := strings.Index(input, "\n")
		if firstNewline != -1 {
			// 找到最后一个 ``` 的位置
			lastBacktick := strings.LastIndex(input, "```")
			if lastBacktick != -1 && lastBacktick > firstNewline {
				// 提取中间内容
				return input[firstNewline+1 : lastBacktick]
			}
		}
	}
	// 否则直接返回
	return input
}

func extraReport(s string) (*re.Report, error) {
	if s == "" {
		return &re.Report{}, errorx.New(errno.InvalidModelOutPut)
	}

	// 中间结构体：与 mapper/report.Report 相同，但 emotion 为 string
	type extra struct {
		Title     string   `json:"title"`
		Keywords  []string `json:"keywords,omitempty"`
		Digest    string   `json:"digest,omitempty"`
		Emotion   string   `json:"emotion,omitempty"`
		Body      string   `json:"body,omitempty"`
		NeedAlarm bool     `json:"need_alarm,omitempty"`
		// 允许携带额外字段，防止解析失败
		Extra map[string]interface{} `json:"-"`
	}

	var e extra
	if err := sonic.Unmarshal([]byte(s), &e); err != nil {
		return nil, err
	}

	rpt := &re.Report{
		Title:     e.Title,
		Keywords:  e.Keywords,
		Digest:    e.Digest,
		Emotion:   enum.EmotionS2i(e.Emotion),
		Body:      e.Body,
		NeedAlarm: e.NeedAlarm,
	}
	return rpt, nil
}

func rptUsage(msg *schema.Message) *core.LLMUsage {
	if msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
		return &core.LLMUsage{
			PromptTokens: msg.ResponseMeta.Usage.PromptTokens,
			PromptTokenDetails: core.PromptTokenDetails{
				CachedTokens: msg.ResponseMeta.Usage.PromptTokenDetails.CachedTokens,
			},
			CompletionTokens: msg.ResponseMeta.Usage.CompletionTokens,
			TotalTokens:      msg.ResponseMeta.Usage.TotalTokens,
		}
	}
	return nil
}

func reverse(msgs []*message.Message) {
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
}
