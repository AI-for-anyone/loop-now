#!/usr/bin/env bash
# 在本机 PostgreSQL 中幂等地创建 Loop Now 的开发与测试数据库。
#
# 只创建以下三项，已存在就跳过，不修改、不删除任何已有对象：
#   - 角色 loop_now（LOGIN，带密码，NOSUPERUSER NOCREATEDB NOCREATEROLE）
#   - 数据库 loop_now_dev、loop_now_test（属主为 loop_now）
#
# 用法：
#   LOOP_NOW_DB_PASSWORD='...' scripts/dev/init-local-db.sh
#
# 以当前系统用户通过本地 socket 连接管理库（默认 postgres，可用 PGADMIN_DATABASE 覆盖），
# 该用户需要有创建角色和数据库的权限。密码从环境变量读入 psql，不出现在命令行参数中。
set -euo pipefail

: "${LOOP_NOW_DB_PASSWORD:?请先设置 LOOP_NOW_DB_PASSWORD}"

admin_db="${PGADMIN_DATABASE:-postgres}"

psql -X -v ON_ERROR_STOP=1 -q -d "$admin_db" <<'SQL'
\set role loop_now
\getenv pw LOOP_NOW_DB_PASSWORD

SELECT format('CREATE ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE', :'role', :'pw')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'role') \gexec

SELECT format('CREATE DATABASE %I OWNER %I', 'loop_now_dev', :'role')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'loop_now_dev') \gexec

SELECT format('CREATE DATABASE %I OWNER %I', 'loop_now_test', :'role')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'loop_now_test') \gexec

SELECT r.rolname AS role, r.rolsuper AS superuser, r.rolcreatedb AS createdb,
       string_agg(d.datname, ', ' ORDER BY d.datname) AS owned_databases
FROM pg_roles r
LEFT JOIN pg_database d ON d.datdba = r.oid AND d.datname IN ('loop_now_dev', 'loop_now_test')
WHERE r.rolname = :'role'
GROUP BY r.rolname, r.rolsuper, r.rolcreatedb;
SQL
