# 休眠的浏览器集成：在仓库根目录下跑 internal/server/app 的 TestWebSleepIntegration，普通与 -race 各一轮，再 go vet。
# App 由测试在进程内构建，数据放在测试自己的临时 SQLite 数据目录里，测试结束时删除；不起 PostgreSQL 或 Redis。
import os,subprocess,argparse
parser=argparse.ArgumentParser();parser.add_argument("--run",default="TestWebSleepIntegration$");mode=parser.add_mutually_exclusive_group();mode.add_argument("--ordinary-only",action="store_true");mode.add_argument("--race-only",action="store_true");args=parser.parse_args();ordinary_only=args.ordinary_only;race_only=args.race_only
env={k:os.environ[k] for k in ["PATH","HOME","TMPDIR"] if k in os.environ};env["LOOP_NOW_WEB_SLEEP_INTEGRATION"]="1";env['PATH']='/Users/ljl/.local/node-v24.21.0/bin:'+env['PATH']
expr=args.run
packages=['./internal/server/app']
for race in ([False] if ordinary_only else [True] if race_only else [False,True]):
 env['LOOP_NOW_WEB_SLEEP_PHASE']='race' if race else 'ordinary'
 print('PHASE', env['LOOP_NOW_WEB_SLEEP_PHASE'],flush=True)
 args=['go','test']+(['-race'] if race else [])+packages+['-run',expr,'-count=1','-v']
 result=subprocess.run(args,env=env,timeout=240);print('PHASE_EXIT',result.returncode,flush=True)
 if result.returncode:raise SystemExit(result.returncode)
if not ordinary_only:
 print('PHASE vet',flush=True)
 subprocess.run(['go','vet']+packages,env=env,check=True)
print('CHECKS_COMPLETE',flush=True)
