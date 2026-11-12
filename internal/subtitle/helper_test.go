package subtitle

import (
	"os"
	"testing"
)

// readFile 读取文件内容, 供测试断言使用.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("无法读取 %s: %v", path, err)
	}
	return string(data)
}
