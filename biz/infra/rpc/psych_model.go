package rpc

import (
	"github.com/google/wire"
	"github.com/xh-polaris/gopkg/kitex/client"
	model "github.com/xh-polaris/psych-idl/kitex_gen/model/psychmodelservice"
	"github.com/xh-polaris/psych-post/biz/infra/config"
	"sync"
)

var pmOnce sync.Once
var pmClnt model.Client

type IPsychModel interface {
	model.Client
}

type PsychModel struct {
	model.Client
}

var PsychModelSet = wire.NewSet(
	NewPsychModel,
	wire.Struct(new(PsychModel), "*"),
	wire.Bind(new(IPsychModel), new(*PsychModel)),
)

func NewPsychModel(config *config.Config) model.Client {
	pmOnce.Do(func() {
		pmClnt = client.NewClient(config.Name, "psych.model", model.NewClient)
	})
	return pmClnt
}

func GetPsychModel() model.Client {
	if pmClnt == nil {
		pmOnce.Do(func() {
			pmClnt = client.NewClient(config.GetConfig().Name, "psych.model", model.NewClient)
		})
	}
	return pmClnt
}
