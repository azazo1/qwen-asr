#!/usr/bin/env python3

# 生成发布产物 (Go).
# 需要同时复制 scripts/archive.py, scripts/build-version.py 与 scripts/release_common.py.
#
# 仅打包 Go CLI, llama.cpp 由用户安装.
# 每个平台原生构建并运行版本冒烟检查, 不要求构建机安装 llama.cpp.

import os
import shutil
import sys

import release_common as rc

# 只改下面几个常量. Go 没有包版本可读, 仓库还没有版本 tag 时用占位版本兜底.
PROJECT_NAME = "qwen-asr"
MAIN_PACKAGE = "./cmd/qwen-asr"
BINARY_NAME = "qwen-asr"
PACKAGE_VERSION = "0.0.0"
# 冒烟检查用的参数, 要求二进制能报出版本号.
SMOKE_ARGS = ["--version"]

# 支持的目标平台与架构组合.
#
# 每个目标在自己的 CI runner 上构建并运行冒烟检查.
DEFAULT_TARGETS = [
    ("linux", "amd64"),
    ("linux", "arm64"),
    ("darwin", "amd64"),
    ("darwin", "arm64"),
    ("windows", "amd64"),
    ("windows", "arm64"),
]

# go env GOOS / GOARCH 到产物命名 token 的映射.
PLATFORM_BY_GOOS = {"darwin": "macos", "linux": "linux", "windows": "windows"}
ARCH_BY_GOARCH = {"amd64": "x86_64", "arm64": "aarch64"}


def naming_tokens(goos, goarch):
    """把 GOOS / GOARCH 换成产物名里的平台与架构 token."""
    target_platform = PLATFORM_BY_GOOS.get(goos)
    target_arch = ARCH_BY_GOARCH.get(goarch)
    if not target_platform or not target_arch:
        rc.die(f"无法识别目标平台 {goos or '?'}-{goarch or '?'}, 请检查 GOOS / GOARCH")
    return target_platform, target_arch


def host_target():
    """取当前构建机的目标平台与架构."""
    goos = rc.run(["go", "env", "GOOS"])
    goarch = rc.run(["go", "env", "GOARCH"])
    host_os = rc.run(["go", "env", "GOHOSTOS"])
    host_arch = rc.run(["go", "env", "GOHOSTARCH"])
    if (goos, goarch) != (host_os, host_arch):
        rc.die("发布检查需要在目标平台运行二进制, 请清除交叉编译的 GOOS/GOARCH 设置")
    return goos, goarch


def binary_name_for(goos):
    return BINARY_NAME + (".exe" if goos == "windows" else "")


def build(goos, goarch, display_version, binary_path):
    """按指定目标构建二进制, 注入版本号.

    注入路径必须是完整包路径, 所以这里用 go list -m 取当前模块名.
    """
    module = rc.run(["go", "list", "-m"])
    env = os.environ.copy()
    env["CGO_ENABLED"] = "0"
    env["GOOS"] = goos
    env["GOARCH"] = goarch
    rc.run(
        [
            "go",
            "build",
            "-trimpath",
            "-ldflags",
            f"-s -w -X {module}/internal/buildinfo.version={display_version}",
            "-o",
            binary_path,
            MAIN_PACKAGE,
        ],
        env=env,
        capture=False,
    )


def build_one(goos, goarch, display_version, archive_version):
    """构建当前平台, 检查版本和架构后打包."""
    target_platform, target_arch = naming_tokens(goos, goarch)
    print(f"构建 {PROJECT_NAME} {display_version} ({target_platform}-{target_arch})")

    binary = binary_name_for(goos)
    # 每个目标各用一个 staging 目录, 免得上一轮的同名二进制被一起打进归档.
    staging = rc.stage_dir(os.path.join("dist", "stage", f"{target_platform}-{target_arch}"))
    binary_path = os.path.join(staging, binary)

    build(goos, goarch, display_version, binary_path)

    entries = [binary]
    if goos == "windows" and goarch == "arm64":
        # ffi v0.8.0 有 ARM64 资源, 但自动释放入口只覆盖 Windows amd64.
        # 将依赖及许可证随 exe 分发, Windows 加载器会从 exe 所在目录查找.
        ffi_dir = rc.run(["go", "list", "-m", "-f", "{{.Dir}}", "github.com/jupiterrider/ffi"])
        ffi_assets = os.path.join(ffi_dir, "assets", "libffi")
        for source, name in [
            (os.path.join(ffi_assets, "windows_arm64", "libffi-8.dll"), "libffi-8.dll"),
            (os.path.join(ffi_assets, "LICENSE"), "LICENSE-libffi"),
        ]:
            shutil.copyfile(source, os.path.join(staging, name))
            entries.append(name)

    rc.smoke_check([binary_path], SMOKE_ARGS, display_version)
    rc.check_binary_format(binary_path, target_platform)
    rc.check_binary_arch(binary_path, target_platform, target_arch)

    rc.archive(
        staging,
        entries,
        PROJECT_NAME,
        archive_version,
        target=(target_platform, target_arch),
    )


def main():
    rc.configure_output_encoding()
    rc.repo_root()

    if sys.argv[1:]:
        rc.die(f"未知参数: {' '.join(sys.argv[1:])}")

    display_version, archive_version = rc.resolve_versions(
        rc.script_path("build-version.py"), PACKAGE_VERSION
    )
    host_goos, host_goarch = host_target()

    if (host_goos, host_goarch) not in DEFAULT_TARGETS:
        rc.die(f"当前目标 {host_goos}-{host_goarch} 不在产物命名表内, 请补充 GOOS / GOARCH 映射")

    build_one(host_goos, host_goarch, display_version, archive_version)


if __name__ == "__main__":
    main()
