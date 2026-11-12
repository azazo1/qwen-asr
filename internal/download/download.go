// Package download 负责把模型权重取到本地.
//
// 有两条路径:
//   - 本机已经装了 modelscope 或 hf 命令行时, 交给它们下载, 复用其缓存与鉴权.
//   - 都没有时, 用内置的并发下载器直接抓取, 支持断点续传.
package download

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/azazo1/qwen-asr/internal/config"
	"github.com/azazo1/qwen-asr/internal/model"
)

// ExternalCLI 是可以代劳下载的外部命令行.
type ExternalCLI struct {
	// Name 是命令行名字, 例如 modelscope 或 hf.
	Name string
	// Path 是解析后的可执行文件路径.
	Path string
}

// 外部命令行的名字.
const (
	CLIModelScope  = "modelscope"
	CLIHuggingFace = "hf"
)

// DetectExternalCLI 按优先级探测可用的下载命令行.
//
// 返回顺序固定: 先 modelscope 后 hf, 便于调用方直接取第一个.
func DetectExternalCLI() []ExternalCLI {
	var out []ExternalCLI
	for _, name := range []string{CLIModelScope, CLIHuggingFace} {
		if p, err := exec.LookPath(name); err == nil {
			out = append(out, ExternalCLI{Name: name, Path: p})
		}
	}
	return out
}

// Command 返回该命令行下载指定仓库到目标目录的完整命令.
//
// 两个 CLI 的参数并不相同, 这里分别拼接, 便于调用前原样展示给用户.
func (c ExternalCLI) Command(repo, localDir string) []string {
	switch c.Name {
	case CLIHuggingFace:
		return []string{c.Path, "download", repo, "--local-dir", localDir}
	default:
		return []string{c.Path, "download", "--model", repo, "--local-dir", localDir}
	}
}

// DisplayCommand 把命令拼成便于复制到终端的字符串.
func DisplayCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t\"'") {
			quoted[i] = strconv.Quote(a)
			continue
		}
		quoted[i] = a
	}
	return strings.Join(quoted, " ")
}

// Run 执行下载命令, 输出直通终端以便用户看到进度.
func (c ExternalCLI) Run(ctx context.Context, repo, localDir string) error {
	args := c.Command(repo, localDir)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	// 两个 CLI 都把进度写到 stderr, 直通给用户最直观.
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s 下载失败: %w", c.Name, err)
	}
	return nil
}

// Source 是一个下载源.
type Source struct {
	// Name 是展示用的名字.
	Name string
	// BaseURL 是拼出直链的前缀, 形如 https://host/repo/resolve/branch.
	BaseURL string
}

// SourcesFor 返回某个仓库在各下载源上的直链前缀.
//
// 顺序即尝试顺序: 默认 ModelScope 优先 (国内更快), 可用环境变量改成 HF 优先.
func SourcesFor(repo string, preferred config.DownloadSource) []Source {
	hf := Source{
		Name:    "HuggingFace",
		BaseURL: fmt.Sprintf("https://huggingface.co/%s/resolve/main", repo),
	}
	ms := Source{
		Name:    "ModelScope",
		BaseURL: fmt.Sprintf("https://modelscope.cn/models/%s/resolve/master", repo),
	}

	if preferred == config.SourceHuggingFace {
		return []Source{hf, ms}
	}
	return []Source{ms, hf}
}

// FileURL 拼出某个文件在该源上的直链.
func (s Source) FileURL(name string) string {
	return s.BaseURL + "/" + name
}

// Options 控制内置下载器的行为.
type Options struct {
	// Jobs 是单个文件内部的分块并发数.
	Jobs int
	// Retries 是单块失败后的重试次数.
	Retries int
	// Progress 接收进度回调, 可为 nil.
	Progress func(Progress)
}

// Progress 是一次进度汇报.
type Progress struct {
	// File 是当前文件名.
	File string
	// Done 与 Total 是字节数.
	Done  int64
	Total int64
}

// Percent 返回完成百分比.
func (p Progress) Percent() float64 {
	if p.Total <= 0 {
		return 0
	}
	return float64(p.Done) / float64(p.Total) * 100
}

// 默认参数.
const (
	defaultJobs    = 8
	defaultRetries = 3
	// chunkSize 是分块大小. 太小会放大请求开销, 太大会削弱并发收益.
	chunkSize = 16 << 20
	// progressInterval 限制进度回调频率, 避免刷屏.
	progressInterval = 200 * time.Millisecond
)

// Downloader 用内置 HTTP 客户端下载模型文件.
type Downloader struct {
	// Client 是 HTTP 客户端, 为 nil 时使用默认客户端.
	Client *http.Client
	// Opts 是并发与重试参数.
	Opts Options
}

// New 构造一个下载器.
func New(opts Options) *Downloader {
	if opts.Jobs <= 0 {
		opts.Jobs = defaultJobs
	}
	if opts.Retries <= 0 {
		opts.Retries = defaultRetries
	}

	return &Downloader{
		Client: &http.Client{
			// 不设整体超时: 大文件下载本来就慢. 分块请求各自受 context 控制.
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConnsPerHost:   opts.Jobs * 2,
				IdleConnTimeout:       30 * time.Second,
				TLSHandshakeTimeout:   20 * time.Second,
				ResponseHeaderTimeout: 60 * time.Second,
			},
		},
		Opts: opts,
	}
}

// FetchSpec 下载一档模型的全部文件到目录.
//
// 每个文件先写 .part 再改名, 因此中断后重跑可以续传, 也不会把半个文件当完整文件.
func (d *Downloader) FetchSpec(ctx context.Context, spec model.Spec, dir string, preferred config.DownloadSource) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("无法创建模型目录 %s: %w", dir, err)
	}

	sources := SourcesFor(spec.Repo, preferred)
	for _, f := range spec.Files() {
		if err := d.fetchFile(ctx, sources, f, filepath.Join(dir, f.Name)); err != nil {
			return err
		}
	}
	return nil
}

// fetchFile 依次尝试各源, 直到某个源下载成功.
func (d *Downloader) fetchFile(ctx context.Context, sources []Source, f model.File, dest string) (err error) {
	var lastErr error

	for _, src := range sources {
		url := src.FileURL(f.Name)
		err := d.downloadOne(ctx, url, f, dest)
		if err == nil {
			return nil
		}
		// 用户主动取消时不继续换源.
		if errors.Is(err, context.Canceled) {
			return err
		}
		lastErr = fmt.Errorf("%s 下载 %s 失败: %w", src.Name, f.Name, err)
	}

	if lastErr == nil {
		lastErr = errors.New("没有可用的下载源")
	}
	return lastErr
}

// downloadOne 下载单个文件, 失败时保留 .part 与进度文件供下次续传.
func (d *Downloader) downloadOne(ctx context.Context, url string, f model.File, dest string) error {
	// 已经完整就不重复下载.
	if info, err := os.Stat(dest); err == nil && info.Size() == f.Size {
		return nil
	}

	total, err := d.remoteSize(ctx, url)
	if err != nil {
		return err
	}
	if total != f.Size {
		return fmt.Errorf("远端文件大小 %d 与清单中的 %d 不一致", total, f.Size)
	}

	part := dest + ".part"
	if err := d.downloadChunks(ctx, url, part, total); err != nil {
		return err
	}
	if err := os.Rename(part, dest); err != nil {
		return fmt.Errorf("无法重命名临时文件: %w", err)
	}
	// 进度文件只在传输过程中有意义.
	os.Remove(part + ".progress")
	return nil
}

// remoteSize 通过 HEAD 请求读取内容长度.
//
// 网络抖动导致 HEAD 失败时重试, 否则一次瞬断就会让整个文件无法开始下载.
func (d *Downloader) remoteSize(ctx context.Context, url string) (int64, error) {
	var lastErr error

	for attempt := 0; attempt < d.Opts.Retries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(1<<uint(attempt-1)) * time.Second
			if wait > 5*time.Second {
				wait = 5 * time.Second
			}
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}

		size, err := d.headOnce(ctx, url)
		if err == nil {
			return size, nil
		}
		if errors.Is(err, context.Canceled) {
			return 0, err
		}
		lastErr = err
	}

	return 0, fmt.Errorf("HEAD 请求重试 %d 次仍失败: %w", d.Opts.Retries, lastErr)
}

// headOnce 发一次 HEAD 请求.
func (d *Downloader) headOnce(ctx context.Context, url string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0, err
	}

	resp, err := d.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HEAD 返回状态码 %d", resp.StatusCode)
	}
	if resp.ContentLength <= 0 {
		return 0, errors.New("服务器没有返回内容长度")
	}
	return resp.ContentLength, nil
}

// progressJournal 记录已完成的块, 支持中断续传.
//
// 用独立的小文件而不是靠 .part 大小判断进度, 因为 .part 被预分配成目标大小,
// 单看大小无法区分"已下载"和"尚未写入".
type progressJournal struct {
	path string
	done map[int64]bool
	mu   sync.Mutex
	f    *os.File
}

// openProgressJournal 读取已有进度并打开追加句柄.
func openProgressJournal(path string) (*progressJournal, error) {
	j := &progressJournal{path: path, done: make(map[int64]bool)}

	if f, err := os.Open(path); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			if off, err := strconv.ParseInt(strings.TrimSpace(scanner.Text()), 10, 64); err == nil {
				j.done[off] = true
			}
		}
		f.Close()
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	j.f = f
	return j, nil
}

// isDone 报告某个块是否已完成.
func (j *progressJournal) isDone(offset int64) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.done[offset]
}

// markDone 记录一个完成的块.
func (j *progressJournal) markDone(offset int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.done[offset] {
		return
	}
	j.done[offset] = true
	fmt.Fprintf(j.f, "%d\n", offset)
}

// Close 关闭追加句柄.
func (j *progressJournal) Close() error {
	if j.f == nil {
		return nil
	}
	return j.f.Close()
}

// downloadChunks 把文件切成多个区间并发下载, 已完成的块直接跳过.
func (d *Downloader) downloadChunks(ctx context.Context, url, part string, total int64) error {
	journal, err := openProgressJournal(part + ".progress")
	if err != nil {
		return fmt.Errorf("无法打开下载进度文件: %w", err)
	}
	defer journal.Close()

	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("无法创建临时文件: %w", err)
	}
	defer f.Close()

	// 预分配到目标大小, 让各块可以直接按偏移写入.
	if err := f.Truncate(total); err != nil {
		return fmt.Errorf("无法预分配文件: %w", err)
	}

	// 统计已完成字节, 作为进度初值.
	var done atomic.Int64
	for off := int64(0); off < total; off += chunkSize {
		if journal.isDone(off) {
			end := min(off+chunkSize, total)
			done.Add(end - off)
		}
	}

	stop := d.startProgressReporter(part, total, &done)
	defer stop()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type job struct{ start, end int64 }

	jobs := make(chan job)
	errCh := make(chan error, d.Opts.Jobs)
	var wg sync.WaitGroup

	for i := 0; i < d.Opts.Jobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if err := d.fetchRange(ctx, url, f, j.start, j.end, &done); err != nil {
					errCh <- err
					cancel()
					return
				}
				journal.markDone(j.start)
			}
		}()
	}

	go func() {
		defer close(jobs)
		for start := int64(0); start < total; start += chunkSize {
			if journal.isDone(start) {
				continue
			}
			end := min(start+chunkSize, total)
			select {
			case jobs <- job{start: start, end: end}:
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Wait()
	close(errCh)

	if err := <-errCh; err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// 收尾时确认没有遗漏的块, 防止调度提前退出导致文件不完整.
	missing := int64(0)
	for start := int64(0); start < total; start += chunkSize {
		if !journal.isDone(start) {
			missing++
		}
	}
	if missing > 0 {
		return fmt.Errorf("仍有 %d 个分块未完成, 请重试以续传", missing)
	}
	return nil
}

// startProgressReporter 周期性汇报进度.
func (d *Downloader) startProgressReporter(name string, total int64, done *atomic.Int64) func() {
	if d.Opts.Progress == nil {
		return func() {}
	}

	stopCh := make(chan struct{})
	var once sync.Once

	go func() {
		ticker := time.NewTicker(progressInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				d.Opts.Progress(Progress{
					File:  filepath.Base(strings.TrimSuffix(name, ".part")),
					Done:  min(done.Load(), total),
					Total: total,
				})
			case <-stopCh:
				// 收尾时再报一次, 让进度停在最终值.
				d.Opts.Progress(Progress{
					File:  filepath.Base(strings.TrimSuffix(name, ".part")),
					Done:  min(done.Load(), total),
					Total: total,
				})
				return
			}
		}
	}()

	return func() { once.Do(func() { close(stopCh) }) }
}

// fetchRange 下载一个字节区间, 失败按指数退避重试.
func (d *Downloader) fetchRange(ctx context.Context, url string, f *os.File, start, end int64, done *atomic.Int64) error {
	var lastErr error

	for attempt := 0; attempt < d.Opts.Retries; attempt++ {
		if attempt > 0 {
			// 指数退避, 最多等 5 秒.
			wait := time.Duration(1<<uint(attempt-1)) * time.Second
			if wait > 5*time.Second {
				wait = 5 * time.Second
			}
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		n, err := d.doRange(ctx, url, f, start, end, done)
		if err == nil {
			return nil
		}
		// 已经写进去的字节要退回, 否则重试会重复计数.
		done.Add(-n)
		lastErr = err

		if errors.Is(err, context.Canceled) {
			return err
		}
	}

	return fmt.Errorf("区间 %d-%d 重试 %d 次仍失败: %w", start, end, d.Opts.Retries, lastErr)
}

// doRange 执行一次区间下载, 返回成功写入的字节数.
func (d *Downloader) doRange(ctx context.Context, url string, f *os.File, start, end int64, done *atomic.Int64) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end-1))

	resp, err := d.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	// HF 的直链会 302 到 CDN, Go 会自动跟随; ModelScope 同理.
	if resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("服务器未按区间返回, 状态码 %d", resp.StatusCode)
	}

	// 直接从 start 偏移写入, 与其他块互不干扰.
	w := &offsetWriter{f: f, off: start, end: end}
	n, err := io.Copy(w, &countingReader{r: resp.Body, done: done})
	if err != nil {
		return n, err
	}
	if n != end-start {
		return n, fmt.Errorf("区间只写入 %d 字节, 预期 %d", n, end-start)
	}
	return n, nil
}

// countingReader 在读取时累计进度.
type countingReader struct {
	r    io.Reader
	done *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.done.Add(int64(n))
	}
	return n, err
}

// VerifySHA256 校验文件哈希, 只在上游提供哈希时可用.
func VerifySHA256(path, want string) error {
	if want == "" {
		return fmt.Errorf("该文件没有可用的 sha256, 无法校验 %s", filepath.Base(path))
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("无法打开 %s: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("读取 %s 时失败: %w", path, err)
	}

	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("%s 校验失败: 期望 %s, 实际 %s", filepath.Base(path), want, got)
	}
	return nil
}

// offsetWriter 把写入定位到文件的固定区间, 用于并发下载.
//
// 不依赖 io.NewSectionWriter: 标准库并没有这个函数.
type offsetWriter struct {
	f   *os.File
	off int64
	end int64
}

func (w *offsetWriter) Write(p []byte) (int, error) {
	if w.off >= w.end {
		return 0, errors.New("写入超出预定区间")
	}
	// 末尾块不会被截断, 但仍然限制写入长度, 防止越界覆盖相邻块.
	if room := w.end - w.off; int64(len(p)) > room {
		p = p[:room]
	}

	n, err := w.f.WriteAt(p, w.off)
	w.off += int64(n)
	return n, err
}
