package report

import (
	"github.com/xh-polaris/psych-pkg/app"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/history"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"time"
)

type (
	Report struct {
		ID         primitive.ObjectID `bson:"_id"`
		HisID      primitive.ObjectID `bson:"his_id"`
		Items      []*Item            `bson:"items"`
		Status     int                `bson:"status"`
		CreateTime time.Time          `bson:"create_time"`
		DeleteTime time.Time          `bson:"delete_time,omitempty"`
	}

	// Item 报表分析结果单元
	Item struct {
		// Group 字段分组, 同一个group的
		Group string `json:"group"`
		// Type 字段类型 string, number, array-string, array-number
		Type  string `json:"type"`
		Key   string `json:"key"`
		Value string `json:"value"`
	}
)

func ConvReport(his *history.History, resp *app.Report) *Report {
	var items []*Item
	for _, item := range resp.Items {
		items = append(items, &Item{
			Group: item.Group,
			Type:  item.Type,
			Key:   item.Key,
			Value: item.Value,
		})
	}
	return &Report{
		HisID: his.ID,
		Items: items,
	}
}
