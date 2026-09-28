package report

import (
	"context"
	"time"

	"github.com/xh-polaris/psych-post/biz/conf"
	"github.com/xh-polaris/psych-post/biz/infra/mapper"
	"github.com/zeromicro/go-zero/core/stores/monc"
	"go.mongodb.org/mongo-driver/v2/bson"
)

var _ IMongoMapper = (*mongoMapper)(nil)

var Mapper IMongoMapper

const (
	collection     = "report"
	cacheKeyPrefix = "cache:report:"
)

type IMongoMapper interface {
	mapper.IMongoMapper[Report]
	InsertOne(ctx context.Context, report *Report) error
	FindOneBySegment(ctx context.Context, conversationID bson.ObjectID, start, end time.Time) (*Report, error)
}

type mongoMapper struct {
	mapper.IMongoMapper[Report]
	conn *monc.Model
}

func NewConfigMongoMapper(config *conf.Config) IMongoMapper {
	conn := monc.MustNewModel(config.Mongo.URL, config.Mongo.DB, collection, config.CacheConf)
	m := &mongoMapper{
		IMongoMapper: mapper.NewMongoMapper[Report](conn),
		conn:         conn,
	}
	Mapper = m
	return m
}

func (m *mongoMapper) InsertOne(ctx context.Context, report *Report) error {
	return m.Insert(ctx, report)
}

// FindOneBySegment 返回同一会话、同一消息时间段生成的报表。
// MQ 消息可能被重新投递，此查询用于复用已有的处理中或已完成报表。
func (m *mongoMapper) FindOneBySegment(ctx context.Context, conversationID bson.ObjectID, start, end time.Time) (*Report, error) {
	return m.FindOneByFields(ctx, bson.M{
		"conversation_id": conversationID,
		"start":           start,
		"end":             end,
	})
}
