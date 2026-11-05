# Loop Now Desktop

桌面壳：拉起并守护发行包里的 `loop-server`，窗口加载它在同一端口提供的控制台，菜单栏图标显示状态。项目说明统一见 [MAIN.md](../MAIN.md)，命令与依赖以 [package.json](package.json) 为准，发行包的内容见 `docs/zh/reference/configuration.md`。

开发时用 `scripts/release/build.sh` 产出的发行包，并且必须选一个配置（不选时壳拒绝启动服务端：开发时的默认配置就是装好的 app 自己在用的那一套）：

```sh
LOOP_NOW_DESKTOP_SERVER=<发行包>/loop-server ../web/bin/npm24 run dev -- --profile=<名字或 HOME 之外的临时目录>
```

打包（macOS arm64 的 DMG 与更新用的 zip，ad-hoc 签名；Windows x64 的 NSIS 安装包，不签名；产物在 `../.local-build/desktop/`）：

```sh
LOOP_NOW_DESKTOP_BUNDLE=<发行包的平台目录> ../web/bin/npm24 run dist:mac
LOOP_NOW_DESKTOP_BUNDLE=<Windows 发行包的平台目录> ../web/bin/npm24 run dist:win   # 在 macOS 上也能交叉打出 NSIS 包
```

服务端的 Windows 发行包还没有时，可以用 `npm run fake-bundle -- <目录> --os windows --arch amd64` 搭一个假的（`test/fakeserver`：只监听、写就绪文件、回答 `/healthz`），用来验证打包；单测也用它真的拉起、停掉和清理孤儿（没有 `go` 时跳过）。

配置依次取 `--profile=<名字或绝对路径>`、`LOOP_NOW_DESKTOP_PROFILE`、`~/Library/Application Support/Loop Now/desktop.json`（Windows：`%APPDATA%\Loop Now\desktop.json`）里的 `{"profile": "<名字>"}`（从 Finder 打开时带不了参数）；打包后的 app 都没选时用名为 `default` 的默认配置。是名字时，macOS 上壳的文件、服务端的数据目录与上下文目录分别在 `~/Library/Application Support/Loop Now/<名字>/` 下的 `shell`、`data`、`context`，日志在 `~/Library/Logs/Loop Now/<名字>`，工作根在 `/Users/Shared/loop-now-desktop/<名字>`；Windows 上都在 `%LOCALAPPDATA%\Loop Now\<名字>\` 下的 `shell`、`data`、`context`、`logs`，工作根暂定 `%LOCALAPPDATA%\loop-now-desktop\<名字>`（只在 `src/platform.ts` 的 `windowsWorkRootBase` 一处，等服务端定下 Windows 的约定再改）；是绝对路径时都放在它下面。

平台差异集中在 `src/platform.ts`（标题栏、托盘图标、未读数、命令行工具的安装方式、文件名）与 `src/process.ts`（停服务端、按 pid 加启动时刻核对孤儿）。Windows 上：系统标题栏；托盘用 `assets/tray.ico`，左键打开窗口；没有应用菜单，关窗口（含 Alt+F4）只收进托盘，退出只从托盘走；未读数是任务栏按钮上的圆点加闪烁；git 查 PATH 里的 `git.exe`，没有就提示去 git-scm.com；"安装命令行工具"把 `resources\bin`（里面是复制出来的 `loopctl.exe`）加进用户 PATH；服务端没有优雅停止方式，退出时 `taskkill /T /F` 整棵进程树。这些目录都由壳显式交给服务端，桌面版不使用服务端的默认目录（`~/.loop_now`、`/Users/Shared/loop-now`），除非 desktop.json 为配置指定了目录：`{"profiles": {"default": {"workRoot": "/Users/Shared/loop-now"}}}`，键是配置名（或 `--profile` 写的绝对路径），可写 `workRoot`、`dataDir`、`contextDir`，值是绝对路径，用于从源码安装迁移时沿用原来的工作根。指定的目录会标在壳日志每次启动的第一行和菜单栏的配置行里；这一项写错时配置不可用，不会悄悄用回默认目录。端口第一次由系统选，之后沿用。`--hidden` 只出菜单栏图标，不开窗口。
