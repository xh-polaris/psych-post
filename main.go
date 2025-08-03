package main

import (
	"context"
	"github.com/cloudwego/hertz/pkg/common/hlog"
	logx "github.com/xh-polaris/gopkg/util/log"
	"github.com/xh-polaris/psych-core-api/provider"
	"github.com/xh-polaris/psych-post/biz"
	"github.com/xh-polaris/psych-post/biz/infra/config"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/contrib/propagators/b3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func Init() {
	// 初始化依赖注入
	provider.Init()
	// 初始化自定义日志
	hlog.SetLogger(logx.NewHlogLogger())
	// 设置openTelemetry的传播器，用于分布式追踪中传递上下文信息
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(b3.New(), propagation.Baggage{}, propagation.TraceContext{}))
	http.DefaultTransport = otelhttp.NewTransport(http.DefaultTransport)
}

func main() {
	// 启动后处理程序
	Init()

	// 监听命令行以退出
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := range config.GetConfig().Consumers {
		biz.NewHisConsumer(ctx, i).Consume()
	}
	osSignalHandler(ctx)
}

// osSignalHandler 处理os信号, 监听命令行中止
func osSignalHandler(ctx context.Context) {
	logx.CtxInfo(ctx, "[osSignalHandler] start")
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	logx.CtxInfo(ctx, "[osSignalHandler] receive signal:[%v]", <-ch)
}
