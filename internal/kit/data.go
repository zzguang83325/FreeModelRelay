package kit

import (
	"os"
	"path/filepath"
)

// ResolveDataPath 数据文件统一存放在当前工作目录的 data/ 子目录（相对路径）。
// 约定：先编译成 exe，再在项目根目录下运行。
func ResolveDataPath(filename string) string {
	os.MkdirAll("data", 0755)
	return filepath.Join("data", filename)
}

// FileExists 判断文件是否存在。
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
