package engine

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/azazo1/qwen-asr/internal/enginelib"
)

func fakeLibraries(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, lib := range enginelib.RequiredLibs {
		if err := os.WriteFile(filepath.Join(dir, enginelib.Filename(runtime.GOOS, lib)), []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// 用假的加载器验证候选顺序, 不依赖本机安装或污染全局 FFI 状态.
func TestResolveSelection(t *testing.T) {
	for _, tc := range []struct {
		name                                   string
		explicit, firstFails, allFail, missing bool
		want                                   Origin
		wantErr                                bool
		wantEvents                             []string
	}{
		{name: "system", want: OriginSystem, wantEvents: []string{"first"}},
		{name: "next-system", firstFails: true, want: OriginSystem, wantEvents: []string{"first", "second"}},
		{name: "unusable", allFail: true, wantErr: true, wantEvents: []string{"first", "second"}},
		{name: "missing", missing: true, wantErr: true},
		{name: "explicit", explicit: true, want: OriginExplicit, wantEvents: []string{"first"}},
		{name: "explicit-error", explicit: true, firstFails: true, wantErr: true, wantEvents: []string{"first"}},
		{name: "explicit-missing", explicit: true, missing: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
			if !tc.missing {
				fakeLibraries(t, first)
				fakeLibraries(t, second)
			}
			opts := ResolveOptions{}
			if tc.explicit {
				opts.ExplicitDir = first
			}
			var events []string
			got, err := resolveWith(opts, []string{first, first, second}, func(dir string) error {
				events = append(events, filepath.Base(dir))
				if tc.allFail || (dir == first && tc.firstFails) {
					return errors.New("load failure")
				}
				return nil
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v", err)
			}
			if tc.wantErr && !tc.explicit && !errors.Is(err, ErrEngineNotFound) {
				t.Fatalf("expected ErrEngineNotFound: %v", err)
			}
			if !tc.wantErr && got.Origin != tc.want {
				t.Fatalf("origin = %s", got.Origin)
			}
			if !reflect.DeepEqual(events, tc.wantEvents) {
				t.Fatalf("events = %v, want %v", events, tc.wantEvents)
			}
		})
	}
}
