package errno

import "github.com/xh-polaris/psych-post/pkg/errorx/code"

const (
	ConfigErr = 999_004_000
)

func init() {
	code.Register(
		ConfigErr,
		"配置 {app} 失败",
		code.WithAffectStability(false),
	)
}
