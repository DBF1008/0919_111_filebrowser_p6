#!/bin/sh
# 手动运行本次搜索功能改造涉及的全部单元测试。
# 用法: sh test.sh
set -e

echo "==> go vet"
go vet ./search/... ./files/... ./http/...

echo "==> search 包单元测试(内容检索 / 可中断遍历 / limit+offset 分页)"
go test -v ./search/...

echo "==> files 包单元测试(结果截断 TruncateItems)"
go test -v ./files/...

echo "==> http 包单元测试(SSE searchHandler / limit+offset 参数解析)"
go test -v ./http/...

echo "==> 全部测试通过"
