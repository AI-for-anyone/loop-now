import os,subprocess,tempfile,pathlib,socket,shutil,argparse
parser=argparse.ArgumentParser();parser.add_argument("--run",default="TestWebSleepIntegration$");mode=parser.add_mutually_exclusive_group();mode.add_argument("--ordinary-only",action="store_true");mode.add_argument("--race-only",action="store_true");args=parser.parse_args();ordinary_only=args.ordinary_only;race_only=args.race_only
root=pathlib.Path(tempfile.mkdtemp(prefix='loop-web-sleep-',dir='/private/tmp'));pgbin='/opt/homebrew/opt/postgresql@18/bin';pgstarted=False;redis=None
try:
 subprocess.run([pgbin+'/initdb','-D',str(root/'pg'),'-U','loop_schema_test','-A','trust','--no-locale','-E','UTF8','--no-sync'],check=True,stdout=subprocess.DEVNULL)
 subprocess.run([pgbin+'/pg_ctl','-D',str(root/'pg'),'-l',str(root/'pg.log'),'-o',f"-F -k {root} -h '' -p 55484",'-w','start'],check=True,stdout=subprocess.DEVNULL);pgstarted=True
 subprocess.run([pgbin+'/createdb','-h',str(root),'-p','55484','-U','loop_schema_test','loop_now_test'],check=True)
 with socket.socket() as listener: listener.bind(('127.0.0.1',0));port=listener.getsockname()[1]
 with (root/'redis.log').open('wb') as log:
  redis=subprocess.Popen(['/opt/homebrew/bin/redis-server','--bind','127.0.0.1','--port',str(port),'--save','','--appendonly','no','--dir',str(root)],stdout=log,stderr=log)
 env={k:os.environ[k] for k in ["PATH","HOME","TMPDIR"] if k in os.environ};env["LOOP_NOW_WEB_SLEEP_INTEGRATION"]="1";env['LOOP_NOW_TEST_DATABASE_URL']=f'postgres://loop_schema_test@/loop_now_test?host={root}&port=55484';env['LOOP_NOW_TEST_REDIS_URL']=f'redis://127.0.0.1:{port}/10';env['PATH']='/Users/ljl/.local/node-v24.21.0/bin:'+env['PATH']
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
finally:
 if redis:
  redis.terminate()
  try:redis.wait(timeout=5)
  except subprocess.TimeoutExpired:redis.kill();redis.wait()
 if pgstarted:subprocess.run([pgbin+'/pg_ctl','-D',str(root/'pg'),'-m','immediate','-w','stop'],check=True,stdout=subprocess.DEVNULL)
 shutil.rmtree(root)
 print('OWNED_TEST_INFRA_CLEANED',flush=True)
