package main

import (
	"net/http"
	"os"
	"smartinsure-eino-backend/internal/config"
	"smartinsure-eino-backend/internal/logx"
	"time"

	"smartinsure-eino-backend/internal/api"
)

func main() {
	settings := config.Load()
	closeLogs, err := logx.Configure(logx.Options{
		FilePath:  settings.LogFilePath,
		ToConsole: settings.LogToConsole,
	})
	if err != nil {
		logx.Printf("日志初始化失败", "log initialization failed", "file_path=%s err=%v", settings.LogFilePath, err)
	} else {
		defer func() {
			if err := closeLogs(); err != nil {
				logx.Printf("日志关闭失败", "log close failed", "file_path=%s err=%v", settings.LogFilePath, err)
			}
		}()
	}

	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":34567"
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           api.NewHandler(nil),
		ReadHeaderTimeout: 10 * time.Second,
	}

	logx.Printf("运行日志", "runtime log", "SmartInsure Go API listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logx.Printf("服务启动失败", "server listen failed", "err=%v", err)
		os.Exit(1)
	}
}
