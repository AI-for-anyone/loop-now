# 私有 PATH launcher ABI v1

当前已按新合同实现权威exec manager，正式012接线及SHA见[managed-launcher-verification.md](managed-launcher-verification.md)。下述010描述符为历史，不接当前PATH。

**接线版本冲突已报Main/server：当前活动design第90–94行已改为服务端私有exec manager（仅有界argv+独立exec capability、服务端持配置并确认Start/Wait）；本文件记录的010描述符直exec实现不满足该新增合同，不可作为最终runtime接线。** 本轮新增固定userId及`ExportLaunchDescriptor`/`StandaloneCommand`导出名的源码尚未重新打包，不能用010旧manifest声称覆盖当前源码；等待唯一ABI明确后收敛，不部署或解除门禁。

这是平台受控工件，不是官方release或公开loopctl命令。生产网络准入仍未通过；入口实现/构建就绪不等于darwin-arm64 supported、Pi接入或真实授权通过。

## 服务端接线

1. 从固定安装manifest核对native、`lark-cli-sandbox-probe`、`lark-cli-launcher`及平台源码的独立SHA。launcher由平台发行阶段构建，不在Agent/运行机现场编译。
2. 以捕获的typed binding和固定版本PG Source创建原`Start` broker。PG Source只返回已加密commit确认的同版本access；本描述符没有grant选择器/真实token/refresh/appSecret。
3. 调用`Broker.LauncherScope(cfg LaunchConfig, launcherSHA256 string) (LauncherDescriptor, error)`。cfg的`Home`和`WorkingDir`为分开的canonical目录，`InputRoots`恰好`[]string{WorkingDir}`。`HostHome`为空时由**服务端**捕获；launcher必须沿用该值，不能把Pi继承的隔离HOME当宿主HOME。
4. JSON编码该返回值，写入同uid、0700私有目录下的0600普通单硬链接文件；目录及文件路径均不得有符号链接，文件上限16KiB。文件必须在业务workspace外；建议放执行scope的私有配置目录。原子发布、不得原地重写正在使用的描述符。调用方负责文件/目录生命周期。
5. Pi scope环境只增加`LOOP_NOW_LARK_SCOPE_FILE=/固定绝对路径/scope.json`。允许模板PATH中的`lark-cli` wrapper必须使用固定绝对launcher入口并保留参数边界，例如：

   ```sh
   #!/bin/sh
   exec '/平台固定安装目录/lark-cli-launcher' "$@"
   ```

   这是server生成的wrapper模板，非可直接执行的安装路径。不要从用户argv选择launcher/native/配置文件，不提取Command.Env后裸启native。
6. scope释放时删除描述符、关闭broker/撤销能力，并按既有执行器规则停止/Wait Pi及其命令进程组。描述符删除本身不会杀死已exec的CLI，不能代替这一步。broker仍以服务端捕获的Binding为authority，不接受CLI自填其他grant或current。

## 描述符与执行行为

导出类型位于`internal/server/larksidecar/bootstrap.go`。顶层字段固定为`protocol`、`brokerURL`、`capability`、`appId`、`brand`、`requestBytes`、`launcherSHA256`、`launch`。protocol=`loop-now-lark-launcher-v1`；`launch`沿Go `LaunchConfig`字段名，使用标准`encoding/json`编码导出结构体即可。

launcher只读该环境路径，拒绝未知/重复JSON字段、尾帧、超长/空/错误字段、宽松权限、符号链接、硬链接和FIFO。它校验自身SHA，再调用**同一个`Broker.Command`实现**，完成native/probe摘要、HOME/workspace、参数、有限JSON `@file`快照与真实sandbox能力准入。路径输入根来自捕获workspace；相对`@file`按该workspace解析，不从shell的任意当前目录增加可信根。

成功准入才会`chdir(WorkingDir)`并`exec`该Command的固定`/usr/bin/sandbox-exec`、完整参数与干净环境；不是裸native exec。进程PID/进程组/标准输入输出保留，没有额外常驻launcher父进程。描述符fd为CLOEXEC，不传入native；`LOOP_NOW_LARK_SCOPE_FILE`及其他继承环境不会进入native。native仅取得该scope的签名能力与公开app/brand，真实token保持broker持有。

任一准备失败退出2，stderr仅`lark_user_launcher_rejected`；不回显路径、argv、descriptor、能力或provider原异常。成功exec后的业务退出码/stdout属于native。launcher无fixture endpoint、可选sandbox、静态token或宿主fallback开关。

同uid隔离边界与既有平台一致：0600不防同uid Agent主动读取自己的能力；HMAC能力本身是秘密，不进入日志/普通API。描述符中的路径/hash由server可信安装信息生成；本ABI不是抵抗同uid宿主篡改全部工件的OS安全边界。

## 本轮构建与验证（2026-10-20）

工件目录：`.local-build/lark-user-variant-proof/build-010-launcher/`。使用既存且校验过的固定官方源码归档，无新下载/官方源码修改。

| 项目 | SHA256 |
|---|---|
| native（保持原固定官方源码+authsidecar tag） | `7158eb571a5c1eef593001c5a959f401cd688c858a84e9dc6abc7a789a87dbd1` |
| launcher | `1a78890cf7723b2809eb4d46eb17a66a319e4066b6e6cb7f7e846a46680c879c` |
| sandbox probe | `0451a0632e97c8f99d67e2b0690a6b3d0e86fad513cf90b2b31c4dd002f89670` |
| 包（本机平台计算，非厂商发布SHA） | `7e8d6be4af6c1be8591d3e83500eda635f0d470856bd607aa0398db4115e1de9` |

manifest记录官方source SHA/commit、工具链、所有平台broker生产Go源码SHA、launcher入口源码SHA及构建脚本SHA；包内含native、probe、launcher、manifest、官方LICENSE。此包是未准入的集成工件，不是可部署supported声明。

定向`^TestLauncher`覆盖严格私有帧、重复/未知/尾帧、权限/符号链接/硬链接/FIFO、workspace内描述符拒绝、捕获副本及Close后拒绝导出、自身/native错SHA、管理命令、workspace外`@file`、取消/缺probe。真实`/bin/sh`经PATH wrapper调用**独立构建的生产launcher**，在缺描述符/缺probe/坏命令/越界文件时退出2，marker native未启动、Source=0；继承HOME刻意等于scope HOME。该格是实际bootstrap失败关闭验证，不是实际Pi或native成功路径验证。

普通测试日志：`.local-build/lark-user-variant-proof/launcher-ordinary.log`，PASS 2.354s；race日志`launcher-race.log`，PASS 2.764s，显式设置独立构建launcher，相关测试零skip。`go vet ./internal/server/larksidecar ./scripts/lark-user-variant/launcher`通过。首轮独立单元检查的正例因测试临时父目录权限不是0700而拒绝；仅将自持fixture目录设为0700后通过，没有放宽生产检查。

当前原有IPv6同端口门禁仍失败，launcher沿用该失败关闭路径，未为本轮解除旧native gate或重跑旧矩阵。真实Pi/bash执行器装配、scope文件关闭/进程回收与PG Source接入由server继续；本轮未动现库/服务/用户凭证/真实OAuth/供应商。
