package subtitle

import "testing"

func TestJoinSegmentsChineseNoSpace(t *testing.T) {
	segments := []Segment{
		{Text: "今天天气很好"},
		{Text: "我们去公园散步"},
	}

	got := JoinSegments(segments)
	want := "今天天气很好我们去公园散步"

	if got != want {
		t.Errorf("得到 %q, 预期 %q", got, want)
	}
}

func TestJoinSegmentsEnglishWithSpace(t *testing.T) {
	segments := []Segment{
		{Text: "Hello world."},
		{Text: "This is a test."},
	}

	got := JoinSegments(segments)
	want := "Hello world. This is a test."

	if got != want {
		t.Errorf("得到 %q, 预期 %q", got, want)
	}
}

func TestJoinSegmentsMixed(t *testing.T) {
	// 中文段落之间不加空格, 中英交界处也不应强行插入.
	segments := []Segment{
		{Text: "这个是"},
		{Text: "Python 脚本"},
	}

	got := JoinSegments(segments)
	want := "这个是Python 脚本"

	if got != want {
		t.Errorf("得到 %q, 预期 %q", got, want)
	}
}

func TestJoinSegmentsSkipsEmpty(t *testing.T) {
	segments := []Segment{
		{Text: "第一句"},
		{Text: "   "},
		{Text: "第二句"},
	}

	got := JoinSegments(segments)
	want := "第一句第二句"

	if got != want {
		t.Errorf("得到 %q, 预期 %q", got, want)
	}
}

func TestFormatSRTTime(t *testing.T) {
	cases := []struct {
		seconds float64
		want    string
	}{
		{0, "00:00:00,000"},
		{1.5, "00:00:01,500"},
		{61.25, "00:01:01,250"},
		{3661.001, "01:01:01,001"},
		{-5, "00:00:00,000"}, // 负数夹到 0, 不能让字幕出现负时间.
	}

	for _, c := range cases {
		if got := formatSRTTime(c.seconds); got != c.want {
			t.Errorf("formatSRTTime(%v) = %q, 预期 %q", c.seconds, got, c.want)
		}
	}
}

func TestWriteSRTSkipsEmptyText(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/out.srt"

	segments := []Segment{
		{Index: 0, Start: 0, End: 1, Text: "第一句"},
		{Index: 1, Start: 1, End: 2, Text: ""},
		{Index: 2, Start: 2, End: 3, Text: "第二句"},
	}

	if err := WriteSRT(path, segments); err != nil {
		t.Fatalf("WriteSRT 失败: %v", err)
	}

	content := readFile(t, path)
	// 空文本不应产生空字幕条目, 序号保持连续.
	want := "1\n00:00:00,000 --> 00:00:01,000\n第一句\n\n" +
		"2\n00:00:02,000 --> 00:00:03,000\n第二句\n\n"

	if content != want {
		t.Errorf("srt 内容为\n%q\n预期\n%q", content, want)
	}
}
