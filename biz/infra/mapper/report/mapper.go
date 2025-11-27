package report

import (
	"context"

	"github.com/xh-polaris/psych-post/biz/conf"
	"github.com/zeromicro/go-zero/core/stores/monc"
)

var _ MongoMapper = (*mongoMapper)(nil)

var Mapper MongoMapper

const (
	collection     = "report"
	cacheKeyPrefix = "cache:report:"
)

type MongoMapper interface {
	InsertOne(ctx context.Context, report *Report) error
}

type mongoMapper struct {
	conn *monc.Model
}

func New(config *conf.Config) MongoMapper {
	conn := monc.MustNewModel(config.Mongo.URL, config.Mongo.DB, collection, config.CacheConf)
	Mapper = &mongoMapper{conn: conn}
	return Mapper
}

func (m *mongoMapper) InsertOne(ctx context.Context, report *Report) error {
	_, err := m.conn.InsertOne(ctx, cacheKeyPrefix+report.ID.Hex(), report)
	return err
}
