package history

import (
	"github.com/xh-polaris/psych-pkg/core"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"time"
)

type History struct {
	ID         primitive.ObjectID `bson:"_id"`
	Session    string             `bson:"session"`               // 对话标识
	UnitID     primitive.ObjectID `bson:"unit_id"`               // 单位id
	UserID     primitive.ObjectID `bson:"user_id"`               // 用户ID
	Verify     bool               `bson:"verify"`                // 校验方式
	Info       map[string]any     `bson:"info"`                  // 额外信息
	Config     *core.Config       `bson:"config"`                // 对话配置
	HisEntry   []*core.HisEntry   `bson:"his_entry"`             // 对话记录
	Start      time.Time          `bson:"start"`                 // 对话开始时间
	End        time.Time          `bson:"end"`                   // 对话结束时间
	Status     int                `bson:"status"`                // 记录状态
	CreateTime time.Time          `bson:"create_time"`           // 创建时间
	DeleteTime time.Time          `bson:"delete_time,omitempty"` // 删除时间
}
