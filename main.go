package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/cloudwego/hertz/pkg/common/json"
	logx "github.com/xh-polaris/gopkg/util/log"
	"github.com/xh-polaris/psych-post/biz/application"
	"github.com/xh-polaris/psych-post/biz/application/internalapi"
	"github.com/xh-polaris/psych-post/biz/conf"
	"github.com/xh-polaris/psych-post/biz/domain/report"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/config"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/conversation"
	"github.com/xh-polaris/psych-post/biz/infra/mapper/user"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/contrib/propagators/b3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

func Init() {
	// 初始化自定义日志
	hlog.SetLogger(logx.NewHlogLogger())
	// 设置openTelemetry的传播器，用于分布式追踪中传递上下文信息
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(b3.New(), propagation.Baggage{}, propagation.TraceContext{}))
	http.DefaultTransport = otelhttp.NewTransport(http.DefaultTransport)
	application.Init()
}

func main() {
	// 启动后处理程序
	Init()

	// 启动http server，用于health check
	go startHealthServer()
	time.Sleep(100 * time.Millisecond)

	// 监听命令行以退出
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := conf.GetConfig()
	mgr := report.New(conf.GetConfig().Consumers, config.NewConfigMongoMapper(cfg), user.NewUserMongoMapper(cfg), conversation.NewConversationMongoMapper(cfg))
	mgr.BuildConsumer().StartConsume()
	defer mgr.Close()
	osSignalHandler(ctx)

}

// osSignalHandler 处理os信号, 监听命令行中止
func osSignalHandler(ctx context.Context) {
	logx.CtxInfo(ctx, "[osSignalHandler] start")
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	logx.CtxInfo(ctx, "[osSignalHandler] receive signal:[%v]", <-ch)
}

// 添加健康检查 HTTP 服务
func startHealthServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz)
	listenOn := "0.0.0.0:8080"
	internalToken := ""
	if cfg := conf.GetConfig(); cfg != nil {
		if cfg.ListenOn != "" {
			listenOn = cfg.ListenOn
		}
		if cfg.InternalAPI != nil {
			internalToken = cfg.InternalAPI.Token
		}
	}
	mux.Handle("/internal/v1/reports/generate", internalapi.NewReportHandler(internalToken, report.GenerateOpenAPIReport))

	hlog.Infof("Health check server starting on %s", listenOn)
	if err := http.ListenAndServe(listenOn, mux); err != nil {
		hlog.Errorf("Health check server failed: %v", err)
	}
}

// healthz 健康检查处理函数
func healthz(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := map[string]interface{}{
		"status":  "ok",
		"message": "healthz",
		"pid":     os.Getpid(),
		// Go runtime
		"go_version": runtime.Version(),
		"gomaxprocs": runtime.GOMAXPROCS(0),
		"goroutines": runtime.NumGoroutine(),
		"num_cpu":    runtime.NumCPU(),
		"memory": map[string]interface{}{
			"alloc":        m.Alloc,
			"total_alloc":  m.TotalAlloc,
			"sys":          m.Sys,
			"heap_alloc":   m.HeapAlloc,
			"heap_sys":     m.HeapSys,
			"heap_idle":    m.HeapIdle,
			"heap_inuse":   m.HeapInuse,
			"heap_objects": m.HeapObjects,
			"gc_num":       m.NumGC,
			"gc_pause_ns":  m.PauseTotalNs,
		},
	}

	_ = json.NewEncoder(w).Encode(response)
}
