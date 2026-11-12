package download

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/azazo1/qwen-asr/internal/config"
	"github.com/azazo1/qwen-asr/internal/model"
)

// rangeServer 模拟支持 Range 的文件服务器.
//
// content 是被下载的完整内容, failFirst 指定前多少个请求返回 500,
// 用来验证重试逻辑.
func rangeServer(t *testing.T, content []byte, failFirst int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var failures atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failures.Load() < failFirst {
			failures.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(content)))
			w.WriteHeader(http.StatusOK)
			return
		}

		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(content)
			return
		}

		// 解析 bytes=start-end.
		spec := strings.TrimPrefix(rangeHeader, "bytes=")
		parts := strings.SplitN(spec, "-", 2)
		start, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		end, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if end >= int64(len(content)) {
			end = int64(len(content)) - 1
		}
		if start > end {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}

		chunk := content[start : end+1]
		w.Header().Set("Content-Range",
			fmt.Sprintf("bytes %d-%d/%d", start, end, len(content)))
		w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(chunk)
	}))

	t.Cleanup(srv.Close)
	return srv, &failures
}

// newTestDownloader 造一个并发数很小, 便于覆盖分块路径的下载器.
func newTestDownloader() *Downloader {
	return New(Options{Jobs: 3, Retries: 3})
}

func TestDownloadOneWritesExactContent(t *testing.T) {
	// 内容大于一个分块, 才会真正走到分块并发路径.
	content := make([]byte, chunkSize*2+1234)
	for i := range content {
		content[i] = byte(i % 251)
	}

	srv, _ := rangeServer(t, content, 0)
	dl := newTestDownloader()

	dir := t.TempDir()
	dest := filepath.Join(dir, "file.bin")
	f := model.File{Name: "file.bin", Size: int64(len(content))}

	if err := dl.downloadOne(context.Background(), srv.URL, f, dest); err != nil {
		t.Fatalf("下载失败: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("无法读取结果: %v", err)
	}
	if len(got) != len(content) {
		t.Fatalf("长度 %d, 预期 %d", len(got), len(content))
	}
	for i := range content {
		if got[i] != content[i] {
			t.Fatalf("第 %d 字节为 %d, 预期 %d", i, got[i], content[i])
		}
	}
}

func TestDownloadOneRejectsSizeMismatch(t *testing.T) {
	// 远端大小与清单不符时必须失败, 否则会把错误的权重当成功.
	content := make([]byte, 1024)
	srv, _ := rangeServer(t, content, 0)
	dl := newTestDownloader()

	dir := t.TempDir()
	dest := filepath.Join(dir, "file.bin")
	f := model.File{Name: "file.bin", Size: int64(len(content)) + 1}

	if err := dl.downloadOne(context.Background(), srv.URL, f, dest); err == nil {
		t.Fatal("大小不符时应当失败")
	}
}

func TestDownloadOneRetriesTransientFailures(t *testing.T) {
	content := make([]byte, 512)
	srv, failures := rangeServer(t, content, 2)
	dl := newTestDownloader()

	dir := t.TempDir()
	dest := filepath.Join(dir, "file.bin")
	f := model.File{Name: "file.bin", Size: int64(len(content))}

	if err := dl.downloadOne(context.Background(), srv.URL, f, dest); err != nil {
		t.Fatalf("应当在重试后成功: %v", err)
	}
	if failures.Load() < 2 {
		t.Errorf("预期至少发生 2 次失败, 实际 %d", failures.Load())
	}
}

func TestDownloadOneSkipsExistingFile(t *testing.T) {
	// 目标文件已完整时不该再发请求.
	content := make([]byte, 256)
	srv, failures := rangeServer(t, content, 0)
	dl := newTestDownloader()

	dir := t.TempDir()
	dest := filepath.Join(dir, "file.bin")
	if err := os.WriteFile(dest, content, 0o644); err != nil {
		t.Fatalf("无法预置文件: %v", err)
	}

	f := model.File{Name: "file.bin", Size: int64(len(content))}
	if err := dl.downloadOne(context.Background(), srv.URL, f, dest); err != nil {
		t.Fatalf("已完整时不应失败: %v", err)
	}
	if failures.Load() != 0 {
		t.Errorf("已完整时不应发生失败请求, 实际 %d", failures.Load())
	}
}

func TestDownloadResumesFromJournal(t *testing.T) {
	content := make([]byte, chunkSize*2)
	for i := range content {
		content[i] = byte(i % 97)
	}

	srv, _ := rangeServer(t, content, 0)
	dl := newTestDownloader()

	dir := t.TempDir()
	dest := filepath.Join(dir, "file.bin")
	f := model.File{Name: "file.bin", Size: int64(len(content))}

	// 模拟第一次下载只完成了第一个分块:
	// 造出 .part (已预分配大小并写入第一个块) 与进度文件.
	part := dest + ".part"
	pre := make([]byte, len(content))
	copy(pre, content[:chunkSize])
	if err := os.WriteFile(part, pre, 0o644); err != nil {
		t.Fatalf("无法预置部分文件: %v", err)
	}
	if err := os.WriteFile(part+".progress", []byte("0\n"), 0o644); err != nil {
		t.Fatalf("无法预置进度文件: %v", err)
	}

	// 续传后应当补齐剩余部分, 且第一块内容不被破坏.
	if err := dl.downloadOne(context.Background(), srv.URL, f, dest); err != nil {
		t.Fatalf("续传失败: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("无法读取结果: %v", err)
	}
	for i := range content {
		if got[i] != content[i] {
			t.Fatalf("第 %d 字节为 %d, 预期 %d", i, got[i], content[i])
		}
	}
}

func TestDownloadCancelled(t *testing.T) {
	content := make([]byte, 1024)
	srv, _ := rangeServer(t, content, 0)
	dl := newTestDownloader()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立刻取消

	dir := t.TempDir()
	dest := filepath.Join(dir, "file.bin")
	f := model.File{Name: "file.bin", Size: int64(len(content))}

	if err := dl.downloadOne(ctx, srv.URL, f, dest); err == nil {
		t.Fatal("已取消的上下文应当报错")
	}
}

func TestSourcesForOrder(t *testing.T) {
	// 默认国内优先: ModelScope 在前.
	got := SourcesFor("org/repo", "")
	if got[0].Name != "ModelScope" {
		t.Errorf("默认应优先 ModelScope, 实际 %q", got[0].Name)
	}

	// 显式指定 HF 时顺序反转.
	got = SourcesFor("org/repo", config.SourceHuggingFace)
	if got[0].Name != "HuggingFace" {
		t.Errorf("指定 hf 时应优先 HuggingFace, 实际 %q", got[0].Name)
	}

	// 直链拼接必须带上仓库与文件名.
	url := got[0].FileURL("a.gguf")
	if !strings.Contains(url, "org/repo") || !strings.HasSuffix(url, "a.gguf") {
		t.Errorf("直链拼接不正确: %s", url)
	}
}

func TestExternalCLICommand(t *testing.T) {
	hf := ExternalCLI{Name: CLIHuggingFace, Path: "/bin/hf"}
	args := hf.Command("org/repo", "/tmp/models")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "download org/repo") {
		t.Errorf("hf 命令不正确: %s", joined)
	}

	ms := ExternalCLI{Name: CLIModelScope, Path: "/bin/modelscope"}
	args = ms.Command("org/repo", "/tmp/models")
	joined = strings.Join(args, " ")
	if !strings.Contains(joined, "download --model org/repo") {
		t.Errorf("modelscope 命令不正确: %s", joined)
	}
}

func TestDisplayCommandQuotesSpaces(t *testing.T) {
	got := DisplayCommand([]string{"hf", "download", "-o", "/tmp/a b"})
	if !strings.Contains(got, `"/tmp/a b"`) {
		t.Errorf("含空格的参数应当被引号包裹: %s", got)
	}
}

func TestVerifySHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("无法写文件: %v", err)
	}

	// echo -n hello | sha256sum
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

	if err := VerifySHA256(path, want); err != nil {
		t.Errorf("校验应当通过: %v", err)
	}
	if err := VerifySHA256(path, strings.Repeat("0", 64)); err == nil {
		t.Error("哈希不符时应当报错")
	}
	if err := VerifySHA256(path, ""); err == nil {
		t.Error("没有期望哈希时应当报错")
	}
}
