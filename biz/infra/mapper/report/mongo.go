package report

import (
	"context"
	"github.com/xh-polaris/psych-post/biz/infra/config"
	"github.com/zeromicro/go-zero/core/stores/monc"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"sync"
)

const (
	prefixReportCacheKey = "cache:report"
	CollectionName       = "report"
)

var reportMapper *MongoMapper
var once sync.Once

type MongoMapper struct {
	conn *monc.Model
}

func GetMongoMapper() *MongoMapper {
	once.Do(func() {
		c := config.GetConfig()
		conn := monc.MustNewModel(c.Mongo.URL, c.Mongo.DB, CollectionName, c.Cache)
		reportMapper = &MongoMapper{
			conn: conn,
		}
	})
	return reportMapper
}

func (m *MongoMapper) Insert(ctx context.Context, r *Report) error {
	if r.ID.IsZero() {
		r.ID = primitive.NewObjectID()
	}
	key := prefixReportCacheKey + r.HisID.Hex()
	_, err := m.conn.InsertOne(ctx, key, r)
	return err
}
