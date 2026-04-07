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
	"github.com/xh-polaris/psych-post/biz/domain/wordcld"
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
	for range cm.ConsumersCount {
		cm.Consumers = append(cm.Consumers, mq.NewConsumer(cm.ConnMgr, cm.DoConsume, cm.wg))
	}
	return cm
}

func (cm *ConsumeManager) StartConsume() {
	for i, consumer := range cm.Consumers {
		cfg := &mq.ConsumeConfig{
			PrefetchCount:   1,
			PrefetchSize:    0,
			Global:          false,
			Queue:           conf.GetConfig().RabbitMQ.Queue,
			Consumer:        fmt.Sprintf("psych-his-%d", i),
			AutoAck:         false,
			Exclusive:       false,
			NoLocal:         false,
			NoWait:          false,
			Args:            nil,
			NackMultiple:    false,
			NackRequeue:     true,
			AckMultiple:     false,
			MQErrInterval:   time.Second * 5,
			OnceErrInterval: time.Second * 3,
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

	// 先做无需模型的部分：MetaInfo、获取历史消息、生成关键词词云并写入初始报表（报表状态为Processing）
	oids, err := util.ObjectIDsFromHex(notify.UnitId, notify.UserId, notify.Session)
	if err != nil {
		logs.Errorf("[mq consumer] invalid id in notify: %s", err)
		return false, err
	}

	// 获取聊天记录并按时间正序
	msgs, err := his.Mgr.RetrieveMessage(ctx, notify.Session, -1)
	if err != nil {
		logs.Errorf("[mq consumer] retrieve message err: %s", err)
		return
	}
	reverse(msgs)

	// 统计对话轮数
	rounds := 0
	for _, m := range msgs {
		if m.Role == enum.MsgRoleUser && m.Content != "" {
			rounds++
		}
	}

	// 生成关键词词云（来自 domain/wordcld）
	kwMap, err := wordcld.Extractor.FromHisMsgPercent(msgs)
	if err != nil {
		logs.Errorf("[mq consumer] wordcloud extract err: %s", err)
	}

	// 插入初始报表（Processing），调用模型生成后以 UpdateFields 补全
	rptID := bson.NewObjectID()
	initial := &re.Report{
		ID:             rptID,
		UnitID:         oids[0],
		UserID:         oids[1],
		ConversationID: oids[2],
		Round:          rounds,
		Start:          time.Unix(notify.Start, 0),
		End:            time.Unix(notify.End, 0),
		Config:         nil,
		Info:           notify.Info,
		Keywords:       kwMap,
		Status:         enum.ReportStatusProcessing,
	}
	if err = re.Mapper.InsertOne(ctx, initial); err != nil {
		logs.Error("[mq consumer] insert initial report err:", err)
		return
	}

	// 以下为需要模型的流程：获取配置、创建 client、构造 prompt、调用模型
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
	reportSetting, err := buildReportSetting(conf.GetConfig(), cfg.Report, notify.UserId)
	if err != nil {
		logs.Errorf("[mq consumer] build report config err: %s", err)
		return
	}
	cli, err := app.NewChatApp(ctx, notify.Session, reportSetting)
	if err != nil {
		logs.Errorf("[mq consumer] build report app err: %s", err)
		return
	}

	// 构造报表生成提示词
	prompt, _, err := cm.buildPrompt(ctx, notify, msgs)
	if err != nil {
		logs.Errorf("[mq consumer] build prompt err: %s", err)
		return
	}

	// 调用模型生成报表
	resp, err := cli.Generate(ctx, prompt)
	if err != nil {
		logs.Errorf("[mq consumer] generate err: %s", err)
		return
	}

	// 解析模型输出
	clean := cleanJSONString(resp.Content)
	result, err := extraReport(clean)
	if err != nil {
		logs.Errorf("[mq consumer] unmarshal err: %s, content: %s", err, clean)
		return
	}

	// 使用 UpdateFields 补全初始报表的其余字段
	update := bson.M{
		"title":        result.Title,
		"digest":       result.Digest,
		"emotion":      result.Emotion,
		"body":         result.Body,
		"need_alarm":   result.NeedAlarm,
		"topics":       result.Topics,
		"report_usage": rptUsage(resp),
		"asr_usage":    notify.Usage.ASRUsage,
		"tts_usage":    notify.Usage.TTSUsage,
		"status":       enum.ReportStatusSuccess,
	}
	if err = re.Mapper.UpdateFields(ctx, rptID, update); err != nil {
		logs.Error("[mq consumer] update report err:", err)
		return
	}

	// 可能需要创建预警
	if result.NeedAlarm {
		al := alarm.Alarm{
			ID:             bson.NewObjectID(),
			UnitID:         oids[0],
			UserID:         oids[1],
			ConversationID: oids[2],
			Emotion:        result.Emotion,
			Keywords:       util.KeywordsMap2Slice(initial.Keywords),
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
	oid, err := util.ObjectIDsFromHex(notify.UserId)
	if err != nil {
		logs.Errorf("[mq consumer] invalid userId: %s", err)
		return nil, 0, err
	}
	usr, err := cm.UserMapper.FindOneById(ctx, oid[0])
	if err != nil {
		logs.Errorf("[mq consumer] get user err: %s", err)
		return nil, 0, err
	}

	infoStr := fmt.Sprintf("学生基本信息:\n学生姓名:%s\n班级:%d年级%d班\n性别:%s\n", usr.Name, usr.Grade, usr.Class, enum.GenderI2S[usr.Gender])
	sb.WriteString(infoStr)
	sb.WriteString("对话内容：\n")
	for _, m := range msgs {
		if m.Content != "" { // 消息有效
			count++
			sb.WriteString("<")
			sb.WriteString(enum.MsgRoleItoA[m.Role])
			sb.WriteString(">")
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
		Topics    []string `json:"topics,omitempty"`
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
		Topics:    e.Topics,
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
