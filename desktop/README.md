# Loop Now Desktop

桌面壳：拉起并守护发行包里的 `loop-server`，窗口加载它在同一端口提供的控制台，菜单栏图标显示状态。项目说明统一见 [MAIN.md](../MAIN.md)，命令与依赖以 [package.json](package.json) 为准，发行包的内容见 `docs/zh/reference/configuration.md`。

开发时用 `scripts/release/build.sh` 产出的发行包，并且必须选隔离配置（不选时壳拒绝启动服务端，免得碰到线上实例的 `~/.loop_now` 与 `/Users/Shared/loop-now`）：

```sh
LOOP_NOW_DESKTOP_SERVER=<发行包>/loop-server ../web/bin/npm24 run dev -- --profile=<名字或 HOME 之外的临时目录>
```

`--profile`（或 `LOOP_NOW_DESKTOP_PROFILE`）是名字时放在 `~/Library/Application Support/Loop Now Profiles/<名字>`，工作根在 `/Users/Shared/loop-now-profiles/<名字>`；是绝对路径时壳的设置、日志与服务端的数据目录、上下文目录、工作根都放在它下面。
