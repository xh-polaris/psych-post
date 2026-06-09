package conversation

import (
	"context"
	"time"

	"github.com/xh-polaris/psych-post/biz/conf"
	"github.com/xh-polaris/psych-post/biz/cst"
	"github.com/xh-polaris/psych-post/biz/infra/mapper"
	"github.com/xh-polaris/psych-post/type/enum"
	"github.com/zeromicro/go-zero/core/stores/monc"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	collectionName = "conversation"
)

var _ IMongoMapper = (*mongoMapper)(nil)

type IMongoMapper interface {
	mapper.IMongoMapper[Conversation]
	FindByUserIdAndTimeRange(ctx context.Context, userId bson.ObjectID, start, end time.Time) ([]*Conversation, error)
}

type mongoMapper struct {
	conn *monc.Model
	mapper.IMongoMapper[Conversation]
}

func NewConversationMongoMapper(config *conf.Config) IMongoMapper {
	conn := monc.MustNewModel(config.Mongo.URL, config.Mongo.DB, collectionName, config.CacheConf)
	return &mongoMapper{conn: conn, IMongoMapper: mapper.NewMongoMapper[Conversation](conn)}
}

func (m *mongoMapper) FindByUserIdAndTimeRange(ctx context.Context, userId bson.ObjectID, start, end time.Time) ([]*Conversation, error) {
	filter := bson.M{
		cst.UserID: userId,
		cst.Status: bson.M{cst.NE: enum.ConversationStatusDeleted},
	}
	if !start.IsZero() || !end.IsZero() {
		ct := bson.M{}
		if !start.IsZero() {
			ct[cst.GTE] = start
		}
		if !end.IsZero() {
			ct[cst.LT] = end
		}
		if len(ct) > 0 {
			filter[cst.CreateTime] = ct
		}
	}
	opts := options.Find().SetSort(bson.M{cst.CreateTime: 1})
	return m.FindManyWithOption(ctx, filter, opts)
}
