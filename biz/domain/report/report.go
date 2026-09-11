package report

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/xh-polaris/psych-post/biz/conf"
	"github.com/xh-polaris/psych-post/biz/cst"
	"github.com/xh-polaris/psych-post/biz/domain/his"
	"github.com/xh-polaris/psych-post/biz/domain/prompt"
	"github.com/xh-polaris/psych-post/biz/domain/wordcld"
	_ "github.com/xh-polaris/psych-post/biz/infra/llm"
	impl "github.com/xh-polaris/psych-post/biz/infra/llm/impl"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/alarm"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/config"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/conversation"
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
	ConvMapper   conversation.IMongoMapper
	wg           *sync.WaitGroup
}

type reportInputMessage struct {
	Role    string
	Content string
}

// buildReportInputPrompt 统一管理端与开放接口传入模型的报告上下文格式
func buildReportInputPrompt(studentName, grade, class, gender string, messages []reportInputMessage) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("学生基本信息:\n学生姓名:%s\n班级:%s年级%s班\n性别:%s\n", studentName, grade, class, gender))
	sb.WriteString("对话内容：\n")
	for _, msg := range messages {
		if msg.Content == "" {
			continue
		}
		sb.WriteString("<")
		sb.WriteString(msg.Role)
		sb.WriteString("> ")
		sb.WriteString(msg.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

func New(consumer int, cfgMapper config.IMongoMapper, usrMapper user.IMongoMapper, convMapper conversation.IMongoMapper) *ConsumeManager {
	cm := &ConsumeManager{ConnMgr: mq.NewConnManager(conf.GetConfig().RabbitMQ.Url), ConsumersCount: consumer, wg: &sync.WaitGroup{}, ConfigMapper: cfgMapper, UserMapper: usrMapper, ConvMapper: convMapper}
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

	// 提取 ID：UserId 和 UnitId 从 Info 字典拿，Session 直接从根层级拿
	userId, _ := notify.Info[cst.JsonUserID].(string)
	unitId, _ := notify.Info[cst.JsonUnitID].(string)
	session := notify.Session

	// 先做无需模型的部分：MetaInfo、获取历史消息、生成关键词词云并写入初始报表（报表状态为Processing）
	oids, err := util.ObjectIDsFromHex(unitId, userId, session)
	if err != nil {
		logs.Errorf("[mq consumer] invalid id in notify: %s, notify: %+v", err, notify)
		return false, nil
	}

	// 只读取本报告段内、当前 conversation 的新增消息（[Start, End] 区间，按老师隔离）
	msgs, err := his.Mgr.GetConversationMessages(ctx, oids[2], time.Unix(notify.Start, 0), time.Unix(notify.End, 0))
	if err != nil {
		logs.Errorf("[mq consumer] retrieve report segment messages err: %s", err)
		return
	}

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

	unitOID, userOID, convOID := oids[0], oids[1], oids[2]

	// 插入初始报表（Processing），调用模型生成后以 UpdateFields 补全
	rptID := bson.NewObjectID()
	initial := &re.Report{
		ID:             rptID,
		UnitID:         unitOID,
		UserID:         userOID,
		ConversationID: convOID,
		Round:          rounds,
		Start:          time.Unix(notify.Start, 0),
		End:            time.Unix(notify.End, 0),
		CreateTime:     time.Now(),
		Config:         nil,
		Info:           notify.Info,
		Keywords:       kwMap,
		Status:         enum.ReportStatusProcessing,
	}
	if notify.Character != nil {
		id, _ := bson.ObjectIDFromHex(notify.Character.Id)
		initial.Character = &config.Character{
			ID:    id,
			Name:  notify.Character.Name,
			Voice: notify.Character.Voice,
			Image: notify.Character.Image,
		}
	}
	if err = re.Mapper.InsertOne(ctx, initial); err != nil {
		logs.Error("[mq consumer] insert initial report err:", err)
		return
	}

	// 构建 ChatSetting：provider 固定为 deepseek，凭证从 YAML 读
	dsCfg, ok := conf.GetConfig().ModelConfig.Chat[impl.DeepSeek]
	if !ok {
		logs.Errorf("[mq consumer] deepseek config not found in YAML")
		return
	}
	reportSetting := &app.ChatSetting{
		Provider:  impl.DeepSeek,
		Url:       dsCfg.URL,
		Model:     impl.DefaultDeepSeekModel,
		AccessKey: dsCfg.AccessKey,
		UserId:    userId,
	}
	cli, err := app.NewChatApp(ctx, session, reportSetting)
	if err != nil {
		logs.Errorf("[mq consumer] build report app err: %s", err)
		return
	}

	// 构造报表生成提示词
	prompt, msgCount, err := cm.buildPrompt(ctx, userOID, msgs)
	if err != nil {
		logs.Errorf("[mq consumer] build prompt err: %s", err)
		return
	}
	_ = msgCount

	// 尝试从 prompt 管理器获取 report/post 阶段的模板作为 system prompt
	if sysPrompt, err := cm.buildSystemPrompt(ctx, userOID, msgs); err == nil && sysPrompt != nil {
		prompt = append([]*schema.Message{sysPrompt}, prompt...)
	} else if err != nil {
		logs.Errorf("[mq consumer] build system prompt err: %s", err)
	}

	// 调用模型生成报表，解析失败则重试1次
	var result *re.Report
	var reportUsage *core.LLMUsage
	var raw string
	for attempt := 0; attempt < cst.RetryTimes; attempt++ {
		resp, genErr := cli.Generate(ctx, prompt, impl.WithJSONOutput())
		if genErr != nil {
			logs.Errorf("[mq consumer] generate err: %s", genErr)
			err = genErr
			return
		}
		reportUsage = rptUsage(resp)
		raw = resp.Content
		clean := cleanJSONString(raw)
		result, err = extraReport(clean)
		if err == nil {
			break
		}
		logs.Errorf("[mq consumer] unmarshal err (attempt %d): %s", attempt+1, err)
	}
	if result == nil {
		logs.Errorf("[mq consumer] unmarshal failed after retry")
		logs.Info("[mq consumer] raw llm output: %s", raw)
		return
	}

	// 使用 UpdateFields 补全初始报表的其余字段
	update := bson.M{
		"title":         result.Title,
		"digest":        result.Digest,
		"analysis":      result.Analysis,
		"simple_report": result.SimpleReport,
		"need_alarm":    result.NeedAlarm,
		"emotion":       result.Emotion,
		"report_usage":  reportUsage,
		"asr_usage":     notify.Usage.ASRUsage,
		"tts_usage":     notify.Usage.TTSUsage,
		"status":        enum.ReportStatusSuccess,
	}
	if err = re.Mapper.UpdateFields(ctx, rptID, update); err != nil {
		logs.Error("[mq consumer] update report err:", err)
		return
	}

	// 更新会话标题
	if err = cm.ConvMapper.UpdateFields(ctx, oids[2], bson.M{"title": result.Title}); err != nil {
		logs.Error("[mq consumer] update conversation title err:", err)
		// 标题更新失败不影响整体报表生成，继续执行
	}

	// 可能需要创建预警
	if result.NeedAlarm {
		al := alarm.Alarm{
			ID:             bson.NewObjectID(),
			UnitID:         unitOID,
			UserID:         userOID,
			ConversationID: convOID,
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

func (cm *ConsumeManager) buildPrompt(ctx context.Context, userIdObj bson.ObjectID, msgs []*message.Message) ([]*schema.Message, int, error) {
	var count int

	// 填充学生信息
	usr, err := cm.UserMapper.FindOneById(ctx, userIdObj)
	if err != nil {
		logs.Errorf("[mq consumer] get user err: %s", err)
		return nil, 0, err
	}

	promptMessages := make([]reportInputMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.Content != "" { // 消息有效
			count++
			promptMessages = append(promptMessages, reportInputMessage{Role: enum.MsgRoleItoA[m.Role], Content: m.Content})
		}
	}
	content := buildReportInputPrompt(usr.Name, fmt.Sprintf("%d", usr.Grade), fmt.Sprintf("%d", usr.Class), enum.GenderI2S[usr.Gender], promptMessages)
	return []*schema.Message{schema.UserMessage(content)}, count, nil
}

type promptMsg struct {
	Role    string
	Content string
	Index   int
}

func (cm *ConsumeManager) buildSystemPrompt(ctx context.Context, userIdObj bson.ObjectID, msgs []*message.Message) (*schema.Message, error) {
	p, err := prompt.Mgr.GetReports(ctx, nil)
	if err != nil {
		return nil, err
	}
	if p == "" {
		p, err = prompt.Mgr.GetTemplates(ctx, "post", nil)
		if err != nil {
			return nil, err
		}
	}
	if p == "" {
		return nil, nil
	}

	usr, err := cm.UserMapper.FindOneById(ctx, userIdObj)
	if err != nil {
		return nil, err
	}

	pmsgs := make([]promptMsg, 0, len(msgs))
	for i, m := range msgs {
		if m.Content != "" {
			pmsgs = append(pmsgs, promptMsg{
				Role:    enum.MsgRoleItoA[m.Role],
				Content: m.Content,
				Index:   i,
			})
		}
	}

	data := map[string]interface{}{
		"StudentName": usr.Name,
		"Grade":       usr.Grade,
		"Class":       usr.Class,
		"Gender":      enum.GenderI2S[usr.Gender],
		"Messages":    pmsgs,
	}

	tmpl, err := template.New("report").Parse(p)
	if err != nil {
		logs.Errorf("[mq consumer] parse prompt template err: %s", err)
		return nil, nil
	}

	var buf bytes.Buffer
	if err = tmpl.Execute(&buf, data); err != nil {
		logs.Errorf("[mq consumer] execute prompt template err: %s", err)
		return nil, nil
	}

	content := strings.TrimSpace(buf.String())
	if content == "" {
		return nil, nil
	}
	return schema.SystemMessage(content), nil
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

	type extra struct {
		Title        string           `json:"title"`
		Digest       string           `json:"digest,omitempty"`
		Analysis     *re.Analysis     `json:"analysis,omitempty"`
		SimpleReport *re.SimpleReport `json:"simple_report,omitempty"`
		ReportAlt    *re.SimpleReport `json:"report,omitempty"`
	}

	var e extra
	if err := sonic.Unmarshal([]byte(s), &e); err != nil {
		return nil, err
	}

	simpleReport := e.SimpleReport
	if simpleReport == nil {
		simpleReport = e.ReportAlt
	}

	needAlarm, emotion := deriveAlarm(simpleReport)

	return &re.Report{
		Title:        e.Title,
		Digest:       e.Digest,
		Analysis:     e.Analysis,
		SimpleReport: simpleReport,
		NeedAlarm:    needAlarm,
		Emotion:      emotion,
	}, nil
}

func deriveAlarm(rpt *re.SimpleReport) (needAlarm bool, emotion int) {
	if rpt == nil {
		return false, enum.UnknownEmotion
	}
	level := strings.TrimSpace(rpt.RiskObservation.Level)
	switch {
	case strings.Contains(level, "高危") || strings.Contains(level, "严重") || strings.Contains(level, "紧急"):
		return true, enum.AlarmEmotionDanger
	case strings.Contains(level, "中") || strings.Contains(level, "较高"):
		return true, enum.AlarmEmotionAnxiety
	case strings.Contains(level, "未发现") || strings.Contains(level, "低") || level == "":
		return false, enum.AlarmEmotionNormal
	default:
		return false, enum.UnknownEmotion
	}
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
