# Manager构造期离线HTTP依赖

2026-10-20，本轮仅实现App跨包隔离fixture所需的显式Go依赖，未改公共HTTP/API、供应商host/path/identity策略、原Manager/local ABI或014工件/pins。

## 冻结入口

```go
type ManagerOptions struct {
    HTTPTransport http.RoundTripper
}
func NewManagerWithOptions(ManagerOptions) *Manager
```

App可信Go构造代码可在其隔离Options中传入自持TLS fixture RoundTripper。Manager在构造时捕获接口到私有字段，OpenScope使用该字段；没有运行期setter、环境变量、可写scope描述符或HTTP请求注入入口。调用方仍需在fixture实现中固定loopback dial和TLS校验，不能把该接口暴露为用户可配置代理。

生产`NewManager()`只委托空ManagerOptions，HTTPTransport=nil。Broker原默认固定HTTPS transport继续`Proxy=nil`，不从环境取代理；按捕获brand核host、RequestURI、身份、HMAC、重放和响应保护不变。显式Go测试transport只替代最后的HTTP依赖，不绕过Command/OS admission、TokenSource、scope创建reservation或真实native consumer计数。

App.Options与真实PG TokenSource装配由server solewriter负责；此切片不新增App配置，不以本测试声称PG/App已经联通。

## 必要真实定向证据

现有`TestManagedNativePATHAndGroupFence`从私有`openScope(..., transport)`改为公开`NewManagerWithOptions(ManagerOptions{HTTPTransport:tr})`再`OpenScope(...)`；其余真实PATH shim→RPC→Command/sandbox→Start/Wait逻辑不降级。固定014三binary+manifest摘要重算一致；TLS transport只允许预期host并dial自持fixture，Source返回合成值，无真实provider请求。

```sh
LARK_USER_VARIANT_BINARY="$PWD/.local-build/lark-user-variant-proof/build-014-seal-deadline/lark-cli" GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go test ./internal/server/larksidecar -run '^TestManagedNativePATHAndGroupFence$' -count=1 -v
LARK_USER_VARIANT_BINARY="$PWD/.local-build/lark-user-variant-proof/build-014-seal-deadline/lark-cli" GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go test -race ./internal/server/larksidecar -run '^TestManagedNativePATHAndGroupFence$' -count=1 -v
go vet ./internal/server/larksidecar
```

ordinary **2.175s**、race **3.232s**，各1项、零skip；vet exit0。真实两页读取、分页间隙active1拒绝整组gate、无scope创建受限、Seal后双listener不可达及Release幂等均保持通过。监听、TLS fixture、native/probe进程及临时目录沿测试cleanup/Close/Wait回收。没有完整旧native矩阵重跑、真实Pi/App/PG新执行、现服务/现库/OAuth/QA或Git操作。

日志与源码稳定摘要：`.local-build/lark-user-variant-proof/manager-transport-options-final/{artifact-check.json,ordinary.log,race.log,vet.log,source-sha256.json}`。此source增量不冒称已经包含在旧014 manifest，也不重建variant包或更改pins。
