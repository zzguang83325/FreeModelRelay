package main

import (
	"FreeModelRelay/internal/app"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"time"
)

func main() {
	host := flag.String("host", "127.0.0.1", "Listen host (0.0.0.0 allows LAN access)")
	port := flag.Int("port", 9999, "Proxy server port")
	startMode := flag.Bool("start", false, "Build, start proxy, and open admin panel in browser")
	flag.Parse()

	// 账号管理（Cline / WorkBuddy / Qoder）一律在管理后台完成，不再提供 CLI 子命令：
	// 各来源的导入方式互不相同，且后台支持测试 / 重置 / 删除，CLI 无法覆盖。
	if *startMode {
		buildAndStart(*host, *port)
		return
	}

	if err := app.StartProxy(*host, *port); err != nil {
		log.Fatalf("Proxy failed: %v", err)
		os.Exit(1)
	}
}

func buildAndStart(host string, port int) {
	exe := "FreeModelRelay.exe"
	if runtime.GOOS != "windows" {
		exe = "./FreeModelRelay"
	}

	fmt.Println("Building proxy...")
	cmd := exec.Command("go", "build", "-o", exe, ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("Build failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Build complete.")

	running := false
	if runtime.GOOS == "windows" {
		out, _ := exec.Command("powershell", "-Command",
			"Get-Process FreeModelRelay -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Id").Output()
		if len(out) > 0 {
			running = true
		}
	}

	if running {
		fmt.Println("Proxy is already running.")
	} else {
		fmt.Println("Starting proxy...")
		startCmd := exec.Command(exe, "-host", host, "-port", fmt.Sprintf("%d", port))
		startCmd.Stdout = os.Stdout
		startCmd.Stderr = os.Stderr
		if err := startCmd.Start(); err != nil {
			fmt.Printf("Start failed: %v\n", err)
			os.Exit(1)
		}
		time.Sleep(2 * time.Second)
		fmt.Println("Proxy started.")
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/admin/", port)
	fmt.Printf("\nAdmin panel: %s\n", url)

	switch runtime.GOOS {
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		exec.Command("open", url).Start()
	default:
		exec.Command("xdg-open", url).Start()
	}
}
