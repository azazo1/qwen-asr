package asrtext

import "testing"

func TestStripLanguagePrefix(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "带语言名与标记",
			raw:  "language English<asr_text>Hello world.",
			want: "Hello world.",
		},
		{
			name: "中文输出",
			raw:  "language Chinese<asr_text>今天天气很好.",
			want: "今天天气很好.",
		},
		{
			name: "粤语输出",
			raw:  "language Cantonese<asr_text>早晨, 你好.",
			want: "早晨, 你好.",
		},
		{
			name: "只有标记没有语言名",
			raw:  "<asr_text>直接是正文",
			want: "直接是正文",
		},
		{
			name: "完全没有前缀",
			raw:  "Hello world.",
			want: "Hello world.",
		},
		{
			name: "空输出",
			raw:  "",
			want: "",
		},
		{
			name: "标记前后有空白",
			raw:  "language English<asr_text>  Hello world.  ",
			want: "Hello world.",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := StripLanguagePrefix(c.raw); got != c.want {
				t.Errorf("StripLanguagePrefix(%q) = %q, 预期 %q", c.raw, got, c.want)
			}
		})
	}
}

func TestStripLanguagePrefixDoesNotEatNormalText(t *testing.T) {
	// 正文里本身含有 "language" 这个词时不能误伤.
	raw := "language English<asr_text>The language model is useful."

	got := StripLanguagePrefix(raw)
	want := "The language model is useful."

	if got != want {
		t.Errorf("得到 %q, 预期 %q", got, want)
	}
}
