package app

import (
	"log"
	"os"
	"strings"
)

// debugEnabled 控制 debug 级日志是否输出。默认关闭 —— 日常运行不该被
// 每分钟一次的同步噪音刷屏。需要排查问题时, 启动前设环境变量 FMR_DEBUG=1
// 即可打开(设 0/false 视为关闭)。
var debugEnabled = func() bool {
	v := strings.TrimSpace(os.Getenv("FMR_DEBUG"))
	if v == "" {
		return false
	}
	switch strings.ToLower(v) {
	case "0", "false", "off", "no":
		return false
	}
	return true
}()

// debugLogf 仅在 debug 开启时输出日志, 用于周期性/可预期的信息
// (如每分钟一次的模型目录同步), 关闭时完全静默。
func debugLogf(format string, v ...any) {
	if !debugEnabled {
		return
	}
	log.Printf(format, v...)
}
