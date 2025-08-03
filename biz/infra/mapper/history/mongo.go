package history

import (
	"context"
	"errors"
	"github.com/xh-polaris/psych-post/biz/infra/config"
	"github.com/xh-polaris/psych-post/biz/infra/consts"
	"github.com/zeromicro/go-zero/core/stores/monc"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"sync"
)

const (
	prefixHistoryCacheKey = "cache:history"
	CollectionName        = "history"
)

var hisMapper *MongoMapper
var once sync.Once

type MongoMapper struct {
	conn *monc.Model
}

func GetMongoMapper() *MongoMapper {
	once.Do(func() {
		c := config.GetConfig()
		conn := monc.MustNewModel(c.Mongo.URL, c.Mongo.DB, CollectionName, c.Cache)
		hisMapper = &MongoMapper{
			conn: conn,
		}
	})
	return hisMapper
}

func (m *MongoMapper) Insert(ctx context.Context, his *History) error {
	if his.ID.IsZero() {
		his.ID = primitive.NewObjectID()
	}
	key := prefixHistoryCacheKey + his.Session
	_, err := m.conn.InsertOne(ctx, key, his)
	return err
}

func (m *MongoMapper) FindOneBySession(ctx context.Context, session string) (his *History, err error) {
	key := prefixHistoryCacheKey + session
	err = m.conn.FindOne(ctx, key, his, bson.M{"session": session})
	switch {
	case err == nil:
		return his, nil
	case errors.Is(err, mongo.ErrNoDocuments):
		return nil, consts.NotFound
	default:
		return nil, err
	}
}
