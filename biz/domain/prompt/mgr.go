package prompt

import (
	"context"
	"strings"
	"time"

	"github.com/xh-polaris/psych-post/biz/infra/cache"
	mapper "github.com/xh-polaris/psych-post/biz/infra/mapper/prompt"
	"github.com/xh-polaris/psych-post/type/enum"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	keySkills    = "prompt:skills"
	keyTplPrefix = "prompt:template:"
	ttl          = time.Hour
)

var Mgr *PromptManager

type PromptManager struct {
	cache  cache.Cmdable
	mapper mapper.IMongoMapper
}

func New(c cache.Cmdable, m mapper.IMongoMapper) {
	Mgr = &PromptManager{cache: c, mapper: m}
}

func (m *PromptManager) GetSkills(ctx context.Context, names ...string) (map[string]string, error) {
	raw, err := m.cache.HGetAll(ctx, keySkills).Result()
	if err == nil && len(raw) > 0 {
		return pickMap(raw, names), nil
	}

	all, err := m.mapper.FindActiveByType(ctx, enum.PromptTypePsychSkill)
	if err != nil {
		return nil, err
	}
	_ = m.cacheSkills(ctx, all)
	mp := make(map[string]string, len(all))
	for _, p := range all {
		mp[p.Name] = p.Content
	}
	return pickMap(mp, names), nil
}

func (m *PromptManager) cacheSkills(ctx context.Context, items []*mapper.Prompt) error {
	if len(items) == 0 {
		return nil
	}
	args := make([]interface{}, 0, len(items)*2)
	for _, p := range items {
		args = append(args, p.Name, p.Content)
	}
	return m.cache.HSet(ctx, keySkills, args...).Err()
}

func pickMap(raw map[string]string, names []string) map[string]string {
	if len(names) == 0 {
		return raw
	}
	out := make(map[string]string, len(names))
	for _, n := range names {
		if v, ok := raw[n]; ok {
			out[n] = v
		}
	}
	return out
}

func (m *PromptManager) GetTemplates(ctx context.Context, stage string, unitID *bson.ObjectID) (string, error) {
	key := keyTplPrefix + stage
	if raw, err := m.cache.Get(ctx, key).Result(); err == nil && raw != "" {
		return raw, nil
	}

	all, err := m.mapper.FindActiveByStageType(ctx, stageInt(stage), enum.PromptTypeTemplate, unitID)
	if err != nil {
		return "", err
	}
	sb := make([]string, len(all))
	for i, p := range all {
		sb[i] = p.Content
	}
	content := strings.Join(sb, "\n")
	_ = m.cache.Set(ctx, key, content, ttl).Err()
	return content, nil
}

func (m *PromptManager) FlushCache(ctx context.Context) {
	_ = m.cache.Del(ctx, keySkills).Err()
	_ = m.cache.Del(ctx, keyTplPrefix+"dialog").Err()
	_ = m.cache.Del(ctx, keyTplPrefix+"post").Err()
	_ = m.cache.Del(ctx, keyTplPrefix+"report").Err()
	_ = m.cache.Del(ctx, keyTplPrefix+"openapi_report").Err()
}

// GetReports 获取报告系统提示词, 与 core-api GetTemplate("report") 共用缓存 key prompt:template:report
func (m *PromptManager) GetReports(ctx context.Context, unitID *bson.ObjectID) (string, error) {
	key := keyTplPrefix + "report"
	if raw, err := m.cache.Get(ctx, key).Result(); err == nil && raw != "" {
		return raw, nil
	}

	p, err := m.mapper.FindActiveByNameType(ctx, enum.PromptTypeReport, enum.PromptTypeReport, unitID)
	if err != nil {
		return "", err
	}
	if p == nil || strings.TrimSpace(p.Content) == "" {
		return "", nil
	}
	_ = m.cache.Set(ctx, key, p.Content, ttl).Err()
	return p.Content, nil
}

// GetOpenAPIReport 优先读取开放接口专用报告模板；未配置时回退默认报告模板。
func (m *PromptManager) GetOpenAPIReport(ctx context.Context) (string, error) {
	key := keyTplPrefix + "openapi_report"
	if raw, err := m.cache.Get(ctx, key).Result(); err == nil && raw != "" {
		return raw, nil
	}
	p, err := m.mapper.FindActiveByNameType(ctx, "openapi_report", enum.PromptTypeReport, nil)
	if err != nil {
		return "", err
	}
	if p != nil && strings.TrimSpace(p.Content) != "" {
		_ = m.cache.Set(ctx, key, p.Content, ttl).Err()
		return p.Content, nil
	}
	return m.GetReports(ctx, nil)
}

func stageInt(s string) string {
	switch s {
	case "dialog":
		return enum.PromptStageDialogue
	case "post":
		return enum.PromptStagePost
	default:
		return ""
	}
}
