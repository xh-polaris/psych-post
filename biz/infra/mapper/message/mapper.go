package message

import (
	"context"
	"errors"
	"time"

	"github.com/xh-polaris/psych-post/biz/conf"
	"github.com/xh-polaris/psych-post/biz/cst"
	"github.com/xh-polaris/psych-post/pkg/errorx"
	"github.com/xh-polaris/psych-post/pkg/logs"
	"github.com/zeromicro/go-zero/core/stores/monc"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var _ IMongoMapper = (*mongoMapper)(nil)

const (
	collection     = "message"
	cacheKeyPrefix = "cache:message:"
)

type IMongoMapper interface {
	RetrieveMessage(ctx context.Context, conversation string, size int) ([]*Message, error)
	FindByConversationIds(ctx context.Context, convIds []bson.ObjectID, opts options.Lister[options.FindOptions]) ([]*Message, error)
	FindByConversationAndTimeRange(ctx context.Context, convID bson.ObjectID, start, end time.Time) ([]*Message, error)
	Insert(ctx context.Context, msg *Message) error
}

type mongoMapper struct {
	conn *monc.Model
}

func NewMessageMongoMapper(config *conf.Config) IMongoMapper {
	conn := monc.MustNewModel(config.Mongo.URL, config.Mongo.DB, collection, config.CacheConf)
	return &mongoMapper{conn: conn}
}

func (m *mongoMapper) RetrieveMessage(ctx context.Context, conversation string, size int) (msgs []*Message, err error) {
	oid, err := bson.ObjectIDFromHex(conversation)
	if err != nil {
		return nil, err
	}

	opts := options.Find().SetSort(bson.M{cst.CreateTime: -1})
	if size > 0 {
		opts.SetLimit(int64(size))
	}
	if err = m.conn.Find(ctx, &msgs, bson.M{cst.ConversationID: oid, cst.Status: bson.M{cst.NE: cst.DeletedStatus}},
		opts); err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		logs.Errorf("[message mapper] find err:%s", errorx.ErrorWithoutStack(err))
		return nil, err
	}
	return msgs, nil
}

func (m *mongoMapper) FindByConversationIds(ctx context.Context, convIds []bson.ObjectID, opts options.Lister[options.FindOptions]) ([]*Message, error) {
	if len(convIds) == 0 {
		return []*Message{}, nil
	}
	var msgs []*Message
	if err := m.conn.Find(ctx, &msgs, bson.M{
		cst.ConversationID: bson.M{cst.In: convIds},
		cst.Status:         bson.M{cst.NE: cst.DeletedStatus},
	}, opts); err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		logs.Errorf("[message mapper] find by conversation ids err:%s", errorx.ErrorWithoutStack(err))
		return nil, err
	}
	return msgs, nil
}

// FindByConversationAndTimeRange 按 conversationId + 时间区间取消息，用于报告段生成。
// 左开右闭 (start, end]，按 create_time 正序返回。
func (m *mongoMapper) FindByConversationAndTimeRange(ctx context.Context, convID bson.ObjectID, start, end time.Time) ([]*Message, error) {
	var msgs []*Message
	filter := bson.M{
		cst.ConversationID: convID,
		cst.Status:         bson.M{cst.NE: cst.DeletedStatus},
		cst.CreateTime:     bson.M{cst.GT: start, cst.LTE: end},
	}
	if err := m.conn.Find(ctx, &msgs, filter, options.Find().SetSort(bson.M{cst.CreateTime: 1})); err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		logs.Errorf("[message mapper] find by conversation and time range err:%s", errorx.ErrorWithoutStack(err))
		return nil, err
	}
	return msgs, nil
}

func (m *mongoMapper) Insert(ctx context.Context, msg *Message) error {
	_, err := m.conn.InsertOneNoCache(ctx, msg)
	return err
}
