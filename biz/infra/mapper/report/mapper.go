package report

import (
	"context"

	"github.com/xh-polaris/psych-post/biz/conf"
	"github.com/xh-polaris/psych-post/biz/infra/mapper"
	"github.com/zeromicro/go-zero/core/stores/monc"
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
