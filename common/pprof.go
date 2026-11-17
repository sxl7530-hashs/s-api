package common

import (
	"fmt"
	"os"
	"runtime/pprof"
	"time"
)

// Monitor 定时监控cpu使用率，超过阈值输出pprof文件
func Monitor() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		<-ticker.C
		if GetSystemStatus().CPUUsage > 80 {
			SysLog("gateway cpu usage too high, capturing cpu profile")
			// write pprof file
			if err := os.MkdirAll("./pprof", 0o755); err != nil {
				SysLog(LogText("failed to create the pprof directory: %s", err.Error()))
				continue
			}
			f, err := os.Create("./pprof/" + fmt.Sprintf("cpu-%s.pprof", time.Now().Format("20060102150405")))
			if err != nil {
				SysLog(LogText("failed to create the pprof file: %s", err.Error()))
				continue
			}
			err = pprof.StartCPUProfile(f)
			if err != nil {
				SysLog(LogText("failed to start pprof: %s", err.Error()))
				_ = f.Close()
				_ = os.Remove(f.Name())
				continue
			}
			time.Sleep(10 * time.Second)
			pprof.StopCPUProfile()
			_ = f.Close()
		}
	}
}
