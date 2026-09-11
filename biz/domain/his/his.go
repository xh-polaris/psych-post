package his

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/bytedance/sonic"
	"github.com/xh-polaris/psych-post/biz/infra/cache"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/conversation"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/message"
	"github.com/xh-polaris/psych-post/biz/infra/util"
	"github.com/xh-polaris/psych-post/pkg/errorx"
	"github.com/xh-polaris/psych-post/pkg/logs"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var Mgr *HistoryManager

const cachePrefix = "psych:msg:"

type HistoryManager struct {
	cache      cache.Cmdable
	mapper     message.IMongoMapper
	convMapper conversation.IMongoMapper
}

func New(c cache.Cmdable, m message.IMongoMapper, convMapper conversation.IMongoMapper) {
	Mgr = &HistoryManager{cache: c, mapper: m, convMapper: convMapper}
}

func dailyCacheKey(userId, date string) string {
	return cachePrefix + userId + ":" + date
}

func hashField(convId string, index int) string {
	return convId + ":" + strconv.Itoa(index)
}

func (h *HistoryManager) GetUserDailyMessages(ctx context.Context, userId, date string) ([]*message.Message, error) {
	key := dailyCacheKey(userId, date)
	if msgs, err := h.RetrieveMessageFromCache(ctx, key); err == nil {
		return msgs, nil
	}

	userOid, err := bson.ObjectIDFromHex(userId)
	if err != nil {
		return nil, err
	}

	start, end, err := util.DayToUTCRange(date)
	if err != nil {
		return nil, err
	}

	convs, err := h.convMapper.FindByUserIdAndTimeRange(ctx, userOid, start, end)
	if err != nil {
		return nil, err
	}
	if len(convs) == 0 {
		return []*message.Message{}, nil
	}

	convIds := make([]bson.ObjectID, len(convs))
	for i, conv := range convs {
		convIds[i] = conv.ID
	}

	msgs, err := h.mapper.FindByConversationIds(ctx, convIds, options.Find().SetSort(bson.M{"create_time": 1}))
	if err != nil {
		return nil, err
	}

	if len(msgs) > 0 {
		_ = h.CacheDailyMessages(ctx, userId, date, msgs)
	}

	sort.Slice(msgs, func(i, j int) bool { return msgs[i].CreateTime.After(msgs[j].CreateTime) })
	return msgs, nil
}

// GetConversationMessages 按 conversationId + 时间区间取本段报告的消息。
// 报告只针对当前会话、本次连接 [Start, End] 区间的新增消息，天然按老师隔离、不跨段重复。
func (h *HistoryManager) GetConversationMessages(ctx context.Context, conversationID bson.ObjectID, start, end time.Time) ([]*message.Message, error) {
	return h.mapper.FindByConversationAndTimeRange(ctx, conversationID, start, end)
}

func (h *HistoryManager) RetrieveMessage(ctx context.Context, convId string, size int) ([]*message.Message, error) {
	oid, err := bson.ObjectIDFromHex(convId)
	if err != nil {
		return nil, err
	}

	conv, err := h.convMapper.FindOneById(ctx, oid)
	if err != nil || conv == nil {
		return h.mapper.RetrieveMessage(ctx, convId, size)
	}

	date := util.FormatDateUTC8(conv.CreateTime)
	userId := conv.UserID.Hex()

	key := dailyCacheKey(userId, date)
	if cached, cacheErr := h.RetrieveMessageFromCache(ctx, key); cacheErr == nil {
		filtered := make([]*message.Message, 0)
		for _, m := range cached {
			if m.ConversationId.Hex() == convId {
				filtered = append(filtered, m)
			}
		}
		if size > 0 && len(filtered) > size {
			return filtered[:size], nil
		}
		return filtered, nil
	}

	return h.mapper.RetrieveMessage(ctx, convId, size)
}

func (h *HistoryManager) RetrieveMessageFromCache(ctx context.Context, key string) ([]*message.Message, error) {
	result, err := h.cache.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, cache.Nil
	}

	msgs := make([]*message.Message, 0, len(result))
	for _, data := range result {
		var msg message.Message
		if err = sonic.Unmarshal([]byte(data), &msg); err != nil {
			logs.Errorf("[his] unmarshal msg err: %s", errorx.ErrorWithoutStack(err))
			return nil, err
		}
		msgs = append(msgs, &msg)
	}
	if len(msgs) > 0 {
		sort.Slice(msgs, func(i, j int) bool { return msgs[i].CreateTime.After(msgs[j].CreateTime) })
	}
	return msgs, nil
}

func (h *HistoryManager) CacheDailyMessages(ctx context.Context, userId, date string, msgs []*message.Message) error {
	key := dailyCacheKey(userId, date)
	fields := make(map[string]string, len(msgs))
	for _, msg := range msgs {
		data, err := sonic.Marshal(msg)
		if err != nil {
			return err
		}
		fields[hashField(msg.ConversationId.Hex(), int(msg.Index))] = string(data)
	}
	p := h.cache.Pipeline()
	p.HSet(ctx, key, fields)
	p.Expire(ctx, key, time.Hour*6)
	_, err := p.Exec(ctx)
	return err
}

func (h *HistoryManager) CacheMessage(ctx context.Context, key string, msgs []*message.Message) error {
	fields := make(map[string]string, len(msgs))
	for _, msg := range msgs {
		data, err := sonic.Marshal(msg)
		if err != nil {
			return err
		}
		fields[fmt.Sprintf("%s%d", key, msg.Index)] = string(data)
	}
	p := h.cache.Pipeline()
	p.HSet(ctx, key, fields)
	p.Expire(ctx, key, time.Hour*6)
	_, err := p.Exec(ctx)
	return err
}

func (h *HistoryManager) AddMessage(ctx context.Context, id string, msg *message.Message) error {
	if err := h.mapper.Insert(ctx, msg); err != nil {
		logs.Errorf("add message err: %s", err)
	}
	_ = h.CacheMessage(ctx, cachePrefix+id, []*message.Message{msg})
	return nil
}
