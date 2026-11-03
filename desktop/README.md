# Loop Now Desktop

桌面壳：拉起并守护发行包里的 `loop-server`，窗口加载它在同一端口提供的控制台，菜单栏图标显示状态。项目说明统一见 [MAIN.md](../MAIN.md)，命令与依赖以 [package.json](package.json) 为准，发行包的内容见 `docs/zh/reference/configuration.md`。

开发时用 `scripts/release/build.sh` 产出的发行包，并且必须选一个配置（不选时壳拒绝启动服务端：开发时的默认配置就是装好的 app 自己在用的那一套）：

```sh
LOOP_NOW_DESKTOP_SERVER=<发行包>/loop-server ../web/bin/npm24 run dev -- --profile=<名字或 HOME 之外的临时目录>
```

打包（macOS arm64 的 DMG 与更新用的 zip，ad-hoc 签名，产物在 `../.local-build/desktop/`）：

```sh
LOOP_NOW_DESKTOP_BUNDLE=<发行包的平台目录> ../web/bin/npm24 run dist:mac
```

配置依次取 `--profile=<名字或绝对路径>`、`LOOP_NOW_DESKTOP_PROFILE`、`~/Library/Application Support/Loop Now/desktop.json` 里的 `{"profile": "<名字>"}`（从 Finder 打开时带不了参数）；打包后的 app 都没选时用名为 `default` 的默认配置。是名字时，壳的文件、服务端的数据目录与上下文目录分别在 `~/Library/Application Support/Loop Now/<名字>/` 下的 `shell`、`data`、`context`，日志在 `~/Library/Logs/Loop Now/<名字>`，工作根在 `/Users/Shared/loop-now-desktop/<名字>`；是绝对路径时都放在它下面。这些目录都由壳显式交给服务端，桌面版从不使用服务端的默认目录（`~/.loop_now`、`/Users/Shared/loop-now`）。端口第一次由系统选，之后沿用。`--hidden` 只出菜单栏图标，不开窗口。
