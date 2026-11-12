// Package buildinfo 保存构建期注入的版本号.
//
// 发布构建通过 -ldflags -X 注入版本号, 例如:
//
//	go build -trimpath -ldflags "-s -w -X $(go list -m)/internal/buildinfo.version=v1.2.3"
//
// 日常开发构建不注入, 显示 dev-build.
package buildinfo

// version 由发布构建通过 -ldflags -X 覆盖, 不要改成常量, 否则链接期无法注入.
var version = "dev-build"

// Version 返回当前构建应当显示的版本号.
func Version() string {
	return version
}
