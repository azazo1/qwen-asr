// Package prompt 提供命令行交互式提问.
//
// 单独成包是为了能在非交互环境里被明确禁用, 也便于独立测试.
package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ErrNotInteractive 表示当前环境不允许交互.
var ErrNotInteractive = errors.New("当前不是交互式终端")

// Prompter 封装交互式提问.
type Prompter struct {
	in      *bufio.Reader
	out     io.Writer
	enabled bool
}

// New 构造一个提问器.
//
// enabled 为 false 时所有提问都会返回 ErrNotInteractive, 调用方据此走非交互分支.
func New(in io.Reader, out io.Writer, enabled bool) *Prompter {
	return &Prompter{
		in:      bufio.NewReader(in),
		out:     out,
		enabled: enabled,
	}
}

// ask 打印问题并读取一行回答.
func (p *Prompter) ask(question string) (string, error) {
	if !p.enabled {
		return "", ErrNotInteractive
	}

	fmt.Fprint(p.out, question)
	line, err := p.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	answer := strings.TrimSpace(line)

	// 读到 EOF 且没有任何内容时, 说明输入已经结束.
	if answer == "" && errors.Is(err, io.EOF) {
		return "", ErrNotInteractive
	}
	return answer, nil
}

// Choose 让用户从若干选项中挑一个, 返回下标.
//
// 直接回车取第一项, 让默认路径最省事.
func (p *Prompter) Choose(title string, options []string) (int, error) {
	if len(options) == 0 {
		return -1, errors.New("没有可选项")
	}

	fmt.Fprintln(p.out)
	fmt.Fprintln(p.out, title)
	for i, opt := range options {
		fmt.Fprintf(p.out, "  %d) %s\n", i+1, opt)
	}

	for {
		answer, err := p.ask(fmt.Sprintf("请输入序号 [1-%d]: ", len(options)))
		if err != nil {
			return -1, err
		}

		if answer == "" {
			return 0, nil
		}

		idx, convErr := strconv.Atoi(answer)
		if convErr == nil && idx >= 1 && idx <= len(options) {
			return idx - 1, nil
		}

		fmt.Fprintf(p.out, "无效输入 %q, 请输入 1 到 %d 之间的序号.\n", answer, len(options))
	}
}

// Confirm 询问一个是/否问题.
func (p *Prompter) Confirm(question string, defaultYes bool) (bool, error) {
	suffix := "[Y/n]"
	if !defaultYes {
		suffix = "[y/N]"
	}

	for {
		answer, err := p.ask(fmt.Sprintf("%s %s ", question, suffix))
		if err != nil {
			return false, err
		}

		switch strings.ToLower(answer) {
		case "":
			return defaultYes, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			fmt.Fprintf(p.out, "请回答 y 或 n, 收到 %q.\n", answer)
		}
	}
}
