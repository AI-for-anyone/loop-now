# Google Python生产切片验证

2026-10-15。Main明确更新本会话授权后实施；plugin仅拥有本目录新增的 `google_auth.py`、`google_runtime.py`、`test_google_auth.py`、`test_google_runtime.py`、本报告。没有修改Go/Store/API/OpenSpec/迁移、既有Codex文件或原安装/浏览器proof；任务由server汇总，不在此勾2.1/2.2/4.3整项。

## 实现与生产边界

`google_auth.py` 按 `google-private-protocol.md` 使用fd3单个JSONL请求及fd4事件/终态；逐字节有界读写，拒重复键、未知字段/非规范类型，frame≤1MiB、完整credential≤64KiB。普通stdout/stderr捕获丢弃，SDK文件日志关闭；固定安全错误和HTTP100–599阶段，不转发raw provider文本。结果写入使用无缓冲os.write完整循环，result_queued后写终态result。

SDK固定568.0.0、bundled google-auth2.50.0、Python3.13.7；只接受平台可信绝对SDK目录，并裁掉user/site/project Python路径。启动必须`-S`，独立HOME/CLOUDSDK_CONFIG位于该HOME且0700；生产拒绝OAUTHLIB_INSECURE_TRANSPORT/PYTHONPATH/宿主ADC等入口。SDK完整树/解释器真实性与安装摘要由server的受控安装清单验证；Python内VERSION检查不替代发行包验证。

登录直接使用官方FullWebFlow，不使用会退到NoBrowserFlow的runner。单端口8085实际绑定后才发URL；官方PKCE/state/换码/scope处理保留，明确prompt=consent。URL固定Google HTTPS issuer/path及已对齐query白名单，拒重复、秘密query、错redirect/response_type/PKCE。官方账号提取匹配后才规范化credential；缺refresh登录失败。父Go负责总deadline，SIGTERM在Python触发固定cancelled并finally关闭监听，不创建子worker。

刷新直接调用官方 `_RefreshGrant`。按server确认的窄适配，单操作内临时将 `_TokenEndpointRequestWithRetry` 指向它的官方`__wrapped__`，finally恢复；底层native requests不跟随redirect，不重放可能已轮换的请求。新的refresh/RAPT保留；未返回新refresh沿输入，未返回RAPT沿输入，返回非法RAPT拒绝。expectedAccount必须先匹配同grant密文account；有id_token再走官方身份提取，无id_token仅沿已绑定grant身份，不冒称重新认证。invalid_grant/RAPT需求返回reauth_required，不打开交互认证。

规范credential仅含协议字段；expiresAt按有限、datetime可表示范围检查后向下取整为UTC正整数毫秒，不延长有效期。实际scopes按响应保留；刷新丢失原批准scope时失败，不能静默缩掉scope。

`google_runtime.py` 保持原SDK gcloud启动链，在固定 `_LoadFromFileOverride` 装配官方Credentials最小子类。初始无token/refresh/client secret；只通过版本匹配selector取得private Unix broker的access+expiry。每次新连接、随机requestId、固定4字段请求，没有project/Agent/task/account/version可选字段；能力/身份由Go绑定。socket0600、父目录0700，同UID，30s总IPC截止、限帧、错ID/多帧/截断/EOF/失联安全失败；服务端按已对齐协议写一帧后close。能力和原响应不进入SDK诊断。

固定google-auth `_make_copy` 会漏掉refresh_handler **和expiry**；子类仅调用super后恢复这两项，保留原官方token及其它复制行为。不能只补handler而令已拿到token的clone因expiry=None永久有效。重复加载仍通过同一selector/能力；不允许原生回退到宿主/当前账号。

## 实际验证与命令

使用前轮已下载的完整官方SDK和私有解包Python，不下载新包、不访问真实Google或DB。测试native子进程启动前置于sandbox：只有本轮自持HTTP fixture端口、callback8085及指定Unix socket放行；空环境/独立HOME/禁宿主凭证与Homebrew读取。合成endpoint/client/code/token/时钟只在测试启动器patch中存在，生产请求没有fixture选项；测试在生产环境检查之后才注入本机HTTP OAuth开关。

```text
python3 -m unittest discover -s auth-helper -p 'test_google_*.py' -v
Ran 10 tests in 7.048s — OK

# 最后补强已拿token后的连续clone/expiry与重复加载断言，仅重跑受影响runtime
python3 -m unittest discover -s auth-helper -p test_google_runtime.py -v
Ran 3 tests — OK

python3 -m py_compile auth-helper/google_auth.py auth-helper/google_runtime.py \
  auth-helper/test_google_auth.py auth-helper/test_google_runtime.py
```

| 通道/场景 | 证据 |
|---|---|
| 实际fd3/fd4 + SDK登录 | 实际bind8085、prompt=consent、回调HTTP200、PKCE verifier与challenge匹配、整数expiry、官方账号提取、完整终态result；ordinary stdout/stderr空 |
| 错state/缺refresh/错账号 | 错state零token POST；缺refresh固定invalid_response；错账号account_mismatch，无credential结果 |
| 占用/取消 | 控制器自持8085时callback_unavailable、零URL/人工回退；绑定后SIGTERM返回cancelled，进程退出、迟到连接被拒 |
| 实际SDK刷新 | 新refresh+新RAPT保留；缺新refresh沿旧；缺id_token沿绑定身份；坏RAPT/坏JSON/invalid_grant/HTTP500/redirect分别固定失败；每格实际token POST精确1次，redirect GET为0 |
| URL/输入 | 非Googleissuer/path、fragment、重复state、秘密/未知query、错端口/type/PKCE等拒绝；严格JSON重复键/NaN、未知字段/错误bool/float/大小与expiry拒绝 |
| Unix私有IPC | 12类response含正确/错ID/未知字段/双帧/截断/超限/坏或过期expiry/超access/forbidden/requires_login/重复键；断连、缩短测试deadline与权限拒绝 |
| 实际两gcloud长命令 | 两个实际生产bootstrap进程各原生`projects list`三页，分页屏障推进仅测试时钟，经历两次后续续期；同波内存broker去重记录[0,1,2]，六个原生HTTP请求的generation逐页匹配 |
| clone与重复加载 | 官方quota/account/universe连续clone保留handler、已取access及expiry；加载点重复取凭证仍保留回调、无refresh/client secret；错selector在加载点被拒 |
| 越selector拒绝 | 实际生产bootstrap的版本selector不匹配时exit1、固定安全stderr，Unix请求0、API请求0 |

这是Python测试及实际多进程并发证据，不是Go race检查；未修改Go包，未重复全仓/Codex/插件测试。施工中曾修正test launcher fd映射碰撞、端口TIME_WAIT的复用选项及测试clock引用被native sys.argv覆盖，均是本地合成fixture问题；最终定向矩阵通过，不把失败记录改称一开始全绿。

## 交接、资源和未完成

server需将两个生产Python文件同目录打包/发布（runtime导入google_auth），使用私有Python`-S`及已证明的framework/library路径，提供0700操作HOME/config和短Unix socket路径；macOS sockaddr路径长度约104字节，不能直接放长repo目录。Go每个resolve响应后close；所有项目/账号/版本/CAS、活动URL清除、refresh独立受限生命周期、未知轮换failclosed及加密提交由server持有。

本次实际runtime broker是隔离测试内存实现，不是PG协调器；没有真实Google/TLS/OAuth/RAPT挑战、Go持久化/取消提交竞态、完整App/Web恢复或系统部署验收。SDK官方账号提取仍遵循其verify=False解析id_token的当前实现，本切片不增加新身份认证契约。SA仍由server的静态官方凭证路径实现，此切片不把SA强制转换为个人grant。

每个测试finally终止/wait子进程、关闭HTTP/Unix socket、join线程并删除独占临时目录；当前无自持常驻资源。旧proof目录继续保留供review。本轮没有现库/服务/用户账号导入/Git动作。源码与测试在本阶段完成后冻结，等待server按协议接Go组件；如ABI变化或review有具体问题再定向修改，不自行扩功能。
