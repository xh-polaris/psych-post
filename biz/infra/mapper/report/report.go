package report

import (
	"strings"
	"time"

	"github.com/xh-polaris/psych-post/biz/infra/mapper/config"
	"github.com/xh-polaris/psych-post/pkg/core"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type Report struct {
	// 基本信息
	ID             bson.ObjectID          `bson:"_id,omitempty" json:"id,omitempty"`                         // 报表id
	UnitID         bson.ObjectID          `bson:"unit_id,omitempty" json:"unitId,omitempty"`                 // 单位id
	UserID         bson.ObjectID          `bson:"user_id,omitempty" json:"userId,omitempty"`                 // 用户id
	ConversationID bson.ObjectID          `bson:"conversation_id,omitempty" json:"conversationId,omitempty"` // 对话id
	TotalUsage     *core.LLMUsage         `bson:"total_usage" json:"totalUsage,omitempty"`                   // 大模型总token消耗
	ReportUsage    *core.LLMUsage         `bson:"report_usage" json:"reportUsage,omitempty"`                 // 报表生成消耗
	ASRUsage       *core.ASRUsage         `bson:"asr_usage,omitempty" json:"asrUsage,omitempty"`             // asr消耗
	TTSUsage       *core.TTSUsage         `bson:"tts_usage,omitempty" json:"ttsUsage,omitempty"`             // tts消耗
	Round          int                    `bson:"round" json:"round"`                                        // 对话中的消息总数
	Start          time.Time              `bson:"start" json:"start"`                                        // 对话开始时间
	End            time.Time              `bson:"end" json:"end"`                                            // 对话结束时间
	CreateTime     time.Time              `bson:"create_time,omitempty" json:"createTime,omitempty"`         // 报表创建时间
	Config         *config.Report         `bson:"config" json:"config,omitempty"`                            // 对话配置
	Info           map[string]interface{} `bson:"info" json:"info,omitempty"`                                // 额外信息
	Character      *config.Character      `bson:"character,omitempty" json:"character,omitempty"`            // 心理老师形象
	Status         int                    `bson:"status" json:"status"`                                      // 报表状态 处理中/已完成/已删除

	// 报表结果
	Title        string             `bson:"title" json:"title"`                                     // 报表标题
	Topics       []string           `bson:"topics" json:"topics,omitempty"`                         // 主要话题-模型生成
	Keywords     map[string]float64 `bson:"keywords" json:"keywords,omitempty"`                     // 关键词&权重-由词云域统计
	Digest       string             `bson:"digest" json:"digest,omitempty"`                         // 对话摘要
	Emotion      int                `bson:"emotion" json:"emotion,omitempty"`                       // v0 兼容字段：仅读取旧报表，新报表不再写入
	Body         string             `bson:"body" json:"body,omitempty"`                             // 正文（Markdown，保留兼容）
	Suggestions  []string           `bson:"suggestions" json:"suggestions,omitempty"`               // 建议与反馈
	NeedAlarm    bool               `bson:"need_alarm" json:"needAlarm,omitempty"`                  // 是否需要创建预警
	Analysis     *Analysis          `bson:"analysis,omitempty" json:"analysis,omitempty"`           // 评估分析（13维度）
	SimpleReport *SimpleReport      `bson:"simple_report,omitempty" json:"simple_report,omitempty"` // 简易报告（v2 瘦身结构）
}

// ======== Analysis — 13 维度状态提取 ========

type Analysis struct {
	Problem     AnalysisProblem    `bson:"problem" json:"problem"`
	Emotion     []AnalysisEmotion  `bson:"emotion" json:"emotion"` // 情绪列表（1-3 项，按影响程度排序）
	Cognition   []string           `bson:"cognition,omitempty" json:"cognition,omitempty"`
	Behavior    []string           `bson:"behavior,omitempty" json:"behavior,omitempty"`
	Duration    string             `bson:"duration" json:"duration"`
	Trigger     []string           `bson:"trigger,omitempty" json:"trigger,omitempty"`
	Coping      []string           `bson:"coping,omitempty" json:"coping,omitempty"`
	Support     AnalysisSupport    `bson:"support" json:"support"`
	HelpSeeking string             `bson:"helpSeeking" json:"helpSeeking"`
	Function    AnalysisFunction   `bson:"function" json:"function"`
	Distress    AnalysisDistress   `bson:"distress" json:"distress"`
	Risk        AnalysisRisk       `bson:"risk" json:"risk"`
	Confidence  AnalysisConfidence `bson:"confidence" json:"confidence"`
	MissingInfo []string           `bson:"missingInfo,omitempty" json:"missingInfo,omitempty"`
}

type ProblemItem struct {
	Category    string `bson:"category" json:"category"`
	Subcategory string `bson:"subcategory" json:"subcategory"`
}

type AnalysisProblem struct {
	Primary   ProblemItem   `bson:"primary" json:"primary"`
	Secondary []ProblemItem `bson:"secondary,omitempty" json:"secondary,omitempty"`
}

type AnalysisEmotion struct {
	Type      string  `bson:"type" json:"type"`
	Intensity float64 `bson:"intensity" json:"intensity"` // 0.5~2.0，中度基准 1.0
}

type AnalysisSupport struct {
	Family              bool     `bson:"family" json:"family"`
	Teacher             bool     `bson:"teacher" json:"teacher"`
	Friend              bool     `bson:"friend" json:"friend"`
	Other               []string `bson:"other,omitempty" json:"other,omitempty"`
	ProtectiveResources []string `bson:"protectiveResources,omitempty" json:"protectiveResources,omitempty"`
	Availability        string   `bson:"availability,omitempty" json:"availability,omitempty"` // 充足 | 一般 | 不足 | 未明确提及
}

type AnalysisFunction struct {
	Learning          string `bson:"learning" json:"learning"`
	Sleep             string `bson:"sleep" json:"sleep"`
	Diet              string `bson:"diet" json:"diet"`
	Interpersonal     string `bson:"interpersonal" json:"interpersonal"`
	EmotionRegulation string `bson:"emotionRegulation" json:"emotionRegulation"`
}

// DistressLevelNum 兼容新旧两版存储的困扰程度：
// 新版为 0-4 数值；旧版为中文字符串（如"2级：中度困扰"），读取时自动归一化为数值。
type DistressLevelNum int32

func (l *DistressLevelNum) UnmarshalBSONValue(typ byte, data []byte) error {
	rv := bson.RawValue{Type: bson.Type(typ), Value: data}
	if rv.Type == bson.TypeString {
		*l = DistressLevelNum(legacyDistressLevel(rv.StringValue()))
		return nil
	}
	var n int32
	if err := rv.Unmarshal(&n); err != nil {
		return err
	}
	*l = DistressLevelNum(n)
	return nil
}

func legacyDistressLevel(s string) int32 {
	switch {
	case strings.Contains(s, "高危"):
		return 4
	case strings.Contains(s, "重度"):
		return 3
	case strings.Contains(s, "中度"):
		return 2
	case strings.Contains(s, "轻度"):
		return 1
	default:
		return 0
	}
}

type AnalysisDistress struct {
	Level  DistressLevelNum `bson:"level" json:"level"` // 0正常波动 | 1轻度 | 2中度 | 3重度 | 4高危
	Reason []string         `bson:"reason,omitempty" json:"reason,omitempty"`
}

type AnalysisRisk struct {
	Level    string      `bson:"level" json:"level"`
	Score    RiskScore   `bson:"score" json:"score"`
	Profile  RiskProfile `bson:"profile" json:"profile"`
	Evidence []string    `bson:"evidence,omitempty" json:"evidence,omitempty"`
	Action   string      `bson:"action" json:"action"`
}

type RiskScore struct {
	CurrentIdeation     int `bson:"currentIdeation" json:"currentIdeation"`
	History             int `bson:"history" json:"history"`
	CurrentStress       int `bson:"currentStress" json:"currentStress"`
	ProtectiveResources int `bson:"protectiveResources" json:"protectiveResources"`
	MentalHealthHistory int `bson:"mentalHealthHistory" json:"mentalHealthHistory"`
	Total               int `bson:"total" json:"total"`
}

type RiskProfile struct {
	CurrentRisk       ProfileSection `bson:"currentRisk" json:"currentRisk"`
	Stressors         ProfileSection `bson:"stressors" json:"stressors"`
	RiskFactors       ProfileSection `bson:"riskFactors" json:"riskFactors"`
	ProtectiveFactors ProfileSection `bson:"protectiveFactors" json:"protectiveFactors"`
	InformationGap    ProfileSection `bson:"informationGap" json:"informationGap"`
	CurrentSafety     struct {
		Summary string `bson:"summary" json:"summary"`
		Status  string `bson:"status" json:"status"`
	} `bson:"currentSafety" json:"currentSafety"`
}

type ProfileSection struct {
	Summary string   `bson:"summary" json:"summary"`
	Items   []string `bson:"items,omitempty" json:"items,omitempty"`
}

type AnalysisConfidence struct {
	Overall string `bson:"overall" json:"overall"`
	Risk    string `bson:"risk" json:"risk"`
	Reason  string `bson:"reason" json:"reason"`
}

// ======== SimpleReport — 瘦身简易报告（v2 提示词产出） ========
// 展示细节由 analysis 与 content 承担，此处仅保留统计与展示必需字段。

type SimpleReport struct {
	Keywords      []string `bson:"keywords,omitempty" json:"keywords,omitempty"` // 2-3 个，源自学生讲到的话题
	Emotion       []string `bson:"emotion" json:"emotion"`                       // 情绪类型列表（1-3 项，与 analysis.emotion 顺序一致）
	RiskLevel     int32    `bson:"riskLevel" json:"riskLevel"`                   // -1未明确 | 0低风险 | 1中低风险 | 2中高风险 | 3高风险
	DistressLevel int32    `bson:"distressLevel" json:"distressLevel"`           // 0正常波动 | 1轻度 | 2中度 | 3重度 | 4高危
	Focus         string   `bson:"focus" json:"focus"`                           // 需要重点关注的问题
	Suggestions   []string `bson:"suggestions" json:"suggestions"`               // 面向老师的 3 条建议
	Content       string   `bson:"content" json:"content"`                       // 报告正文（500-1500字，十六节固定格式）
}
