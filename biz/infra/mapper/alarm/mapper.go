package alarm

import (
	"context"
	"github.com/xh-polaris/psych-post/biz/conf"
	"github.com/xh-polaris/psych-post/biz/cst"
	"github.com/xh-polaris/psych-post/pkg/errorx"
	"github.com/xh-polaris/psych-post/pkg/logs"
	"github.com/zeromicro/go-zero/core/stores/monc"
	"go.mongodb.org/mongo-driver/v2/bson"
)

var _ IMongoMapper = (*mongoMapper)(nil)
var Mapper IMongoMapper

const (
	collection     = "alarm"
	cacheKeyPrefix = "cache:alarm:"
)

type IMongoMapper interface {
	Insert(ctx context.Context, msg *Alarm) error

	Exists(ctx context.Context, id bson.ObjectID) (bool, error)
}

type mongoMapper struct {
	conn *monc.Model
}

func NewAlarmMongoMapper(config *conf.Config) IMongoMapper {
	conn := monc.MustNewModel(config.Mongo.URL, config.Mongo.DB, collection, config.CacheConf)
	return &mongoMapper{conn: conn}
}

func (m *mongoMapper) Insert(ctx context.Context, msg *Alarm) error {
	_, err := m.conn.InsertOneNoCache(ctx, msg)
	return err
}

func (m *mongoMapper) Exists(ctx context.Context, userID bson.ObjectID) (bool, error) {
	c, err := m.conn.CountDocuments(ctx, bson.M{cst.UserID: userID, cst.Status: bson.M{cst.NE: cst.DeletedStatus}})
	if err != nil {
		logs.Errorf("[alarm mapper] find err:%s", errorx.ErrorWithoutStack(err))
		return false, err
	}
	return c > 0, err
}
