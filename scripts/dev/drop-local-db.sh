#!/usr/bin/env bash
# 删除 Loop Now 的本机开发与测试数据库及其角色。
#
# 只删除以下三项，不触碰其他数据库与角色：
#   - 数据库 loop_now_dev、loop_now_test（会断开其上的连接）
#   - 角色 loop_now
#
# 执行前要求输入 loop_now 确认；也可以传 --yes 跳过确认。
set -euo pipefail

admin_db="${PGADMIN_DATABASE:-postgres}"

if [[ "${1:-}" != "--yes" ]]; then
	read -r -p "将删除数据库 loop_now_dev、loop_now_test 与角色 loop_now，输入 loop_now 确认：" answer
	if [[ "$answer" != "loop_now" ]]; then
		echo "已取消" >&2
		exit 1
	fi
fi

psql -X -v ON_ERROR_STOP=1 -q -d "$admin_db" <<'SQL'
DROP DATABASE IF EXISTS loop_now_dev WITH (FORCE);
DROP DATABASE IF EXISTS loop_now_test WITH (FORCE);
DROP ROLE IF EXISTS loop_now;
SQL

echo "已删除 loop_now_dev、loop_now_test 与角色 loop_now"
