package conversation

import (
	"github.com/xh-polaris/psych-post/biz/conf"
	"github.com/xh-polaris/psych-post/biz/infra/mapper"
	"github.com/zeromicro/go-zero/core/stores/monc"
)

const (
	collectionName = "conversation"
)

var _ IMongoMapper = (*mongoMapper)(nil)

type IMongoMapper interface {
	mapper.IMongoMapper[Conversation]
}

type mongoMapper struct {
	conn *monc.Model
	mapper.IMongoMapper[Conversation]
}

func NewConversationMongoMapper(config *conf.Config) IMongoMapper {
	conn := monc.MustNewModel(config.Mongo.URL, config.Mongo.DB, collectionName, config.CacheConf)
	return &mongoMapper{conn: conn, IMongoMapper: mapper.NewMongoMapper[Conversation](conn)}
}
