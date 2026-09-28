package report

import (
	"bytes"
	"context"
	"errors"
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
	"go.mongodb.org/mongo-driver/v2/mongo"
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

const riskLevelOutputInstruction = `输出 simple_report.riskLevel 时必须严格使用以下整数：-1=未明确、0=低风险、1=中低风险、2=中高风险、3=高风险。数值越大风险越高；不得输出其他值。`

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
	logs.Infof("[mq consumer] consume notify received for: {unitId: %s, userId: %s, session: %s}", unitId, userId, session)

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

	unitOID, userOID, convOID := oids[0], oids[1], oids[2]

	// 查询或插入初始报表（Processing）。消息在失败后会被 MQ 重新投递，
	// 必须复用同一会话、同一时间段的记录，避免每次重试都产生一条孤立的 Processing 报表。
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
	existing, findErr := re.Mapper.FindOneBySegment(ctx, convOID, initial.Start, initial.End)
	if findErr == nil {
		if existing.Status == enum.ReportStatusSuccess {
			if err = cm.ensureAlarm(ctx, existing); err != nil {
				logs.Error("[mq consumer] ensure alarm for completed report err:", err)
				return
			}
			return true, nil
		}
		initial = existing
		rptID = existing.ID
		logs.Infof("[mq consumer] reuse report{id=%s} for redelivered notification", rptID.Hex())
	} else if !errors.Is(findErr, mongo.ErrNoDocuments) {
		logs.Error("[mq consumer] find report by segment err:", findErr)
		return
	} else if err = re.Mapper.InsertOne(ctx, initial); err != nil {
		logs.Error("[mq consumer] insert initial report err:", err)
		return
	}
	logs.Info("[mq consumer] report for conversation{id=%s} generating...", session)

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
	prmpt, msgCount, err := cm.buildPrompt(ctx, userOID, msgs)
	if err != nil {
		logs.Errorf("[mq consumer] build prmpt err: %s", err)
		return
	}
	_ = msgCount

	// 尝试从 prmpt 管理器获取 report/post 阶段的模板作为 system prmpt
	if sysPrompt, err := cm.buildSystemPrompt(ctx, userOID, msgs); err == nil && sysPrompt != nil {
		prmpt = append([]*schema.Message{sysPrompt}, prmpt...)
	} else if err != nil {
		logs.Errorf("[mq consumer] build system prmpt err: %s", err)
	}

	// 调用模型生成报表，解析失败则重试1次
	var result *re.Report
	var reportUsage *core.LLMUsage
	var raw string
	for attempt := 0; attempt < cst.RetryTimes; attempt++ {
		resp, genErr := cli.Generate(ctx, prmpt, impl.WithJSONOutput())
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
		logs.Infof("[mq consumer] raw llm output: %s", raw)
		return
	}

	// 使用 UpdateFields 补全初始报表的其余字段
	update := bson.M{
		"title":         result.Title,
		"digest":        result.Digest,
		"analysis":      result.Analysis,
		"simple_report": result.SimpleReport,
		"need_alarm":    result.NeedAlarm,
		"report_usage":  reportUsage,
		"status":        enum.ReportStatusSuccess,
	}
	// Usage 可能为 nil，判空后写入用量字段
	if notify.Usage != nil {
		update["asr_usage"] = notify.Usage.ASRUsage
		update["tts_usage"] = notify.Usage.TTSUsage
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

	initial.NeedAlarm = result.NeedAlarm
	initial.SimpleReport = result.SimpleReport
	if err = cm.ensureAlarm(ctx, initial); err != nil {
		logs.Error("[mq consumer] ensure alarm err:", err)
		return
	}

	return true, nil
}

// ensureAlarm 为已成功生成的报表补齐预警，并保证 MQ 重投不重复创建预警。
func (cm *ConsumeManager) ensureAlarm(ctx context.Context, rpt *re.Report) error {
	if !rpt.NeedAlarm || rpt.SimpleReport == nil {
		return nil
	}

	exists, err := alarm.Mapper.ExistsByReportID(ctx, rpt.ID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	al := alarm.Alarm{
		ID:             bson.NewObjectID(),
		UnitID:         rpt.UnitID,
		UserID:         rpt.UserID,
		ReportID:       rpt.ID,
		ConversationID: rpt.ConversationID,
		Emotion:        rpt.SimpleReport.Emotion,
		Keywords:       rpt.SimpleReport.Keywords,
		Status:         enum.AlarmStatusPending,
		CreateTime:     time.Now(),
	}
	return alarm.Mapper.Insert(ctx, &al)
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
		return schema.SystemMessage(riskLevelOutputInstruction), nil
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
	return schema.SystemMessage(content + "\n\n" + riskLevelOutputInstruction), nil
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

// extraReport 直接将模型输出绑定到 report mapper 的 Report 模型，
// json tag 与提示词 v2 输出约定对齐（title/analysis/simple_report）
func extraReport(s string) (*re.Report, error) {
	if s == "" {
		return &re.Report{}, errorx.New(errno.InvalidModelOutPut)
	}

	rpt := new(re.Report)
	if err := sonic.Unmarshal([]byte(s), rpt); err != nil {
		return nil, err
	}
	if rpt.SimpleReport != nil && (rpt.SimpleReport.RiskLevel < enum.UserRiskLevelUnknown || rpt.SimpleReport.RiskLevel > enum.UserRiskLevelHigh) {
		return nil, errorx.New(errno.InvalidModelOutPut)
	}
	rpt.NeedAlarm = deriveAlarm(rpt.SimpleReport)
	return rpt, nil
}

// deriveAlarm 新报表 riskLevel 为数值：-1未明确 | 0低风险 | 1中低风险 | 2中高风险 | 3高风险。
func deriveAlarm(rpt *re.SimpleReport) (needAlarm bool) {
	if rpt == nil {
		return false
	}
	switch rpt.RiskLevel {
	case 2, 3:
		return true
	default:
		return false
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
