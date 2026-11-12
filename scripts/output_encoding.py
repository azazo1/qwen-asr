#!/usr/bin/env python3

# 脚本共用的标准输出编码配置, 复制到项目 scripts/output_encoding.py.
# 只用标准库.
#
# 构建路径上所有 Python 脚本都要调用它, 包括项目自有脚本 (前端产物同步,
# 图标生成等). Windows 控制台默认是本地代码页 (例如 cp1252), 脚本 print 中文
# 会直接抛 UnicodeEncodeError, 于是那一平台的构建整条失败.
#
# 单独成文件而不是塞进 release_common: 项目自有脚本只该依赖这一个函数,
# 不该为了它去依赖归档与版本解析那一套.

import sys


def configure_output_encoding() -> None:
    """把 stdout 与 stderr 切到 UTF-8, 失败时静默忽略.

    不是所有流都支持 reconfigure (例如被重定向成纯文本句柄时),
    这种场合保持原样即可, 不该因为改不了编码就让脚本退出.
    """
    for stream in (sys.stdout, sys.stderr):
        try:
            stream.reconfigure(encoding="utf-8")
        except (AttributeError, ValueError, OSError):
            pass
