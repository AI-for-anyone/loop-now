#!/usr/bin/env bash
# 发布构建：为本机平台产出 loop-now 发行目录与压缩包，并写出校验和。发行目录里有：
#   loop-server            内嵌控制台的服务端
#   loopctl-dist/<版本>/    各平台的 loopctl 与它的 SHA256SUMS、manifest.json
#   plugins/host/main.js   插件宿主；plugins/official/ 官方插件
#   runtime/node/          官方 Node（带 npm）；runtime/pi/ 用它安装的 Pi
# loop-server 没有另外配置时就用这些随包的文件（见 internal/server/config 的 Bundled*），解开即可运行。
#
#   scripts/release/build.sh [--only <os>/<arch>] [--smoke] <版本> <输出目录>
#
# Pi 的依赖要在目标架构上安装，所以只构建本机平台；--only 用来确认目标就是本机。--smoke 用产物实际启动一次。
# 需要：Go、Node 24.21.0（构建控制台与插件宿主）、git、curl，以及网络（下载 Node、安装依赖与 Pi）。
set -euo pipefail

NODE_VERSION=24.21.0
PI_VERSION=1.4.0

only="" smoke=0
while [ $# -gt 0 ]; do
	case "$1" in
	--only) only="$2"; shift 2 ;;
	--smoke) smoke=1; shift ;;
	-*) echo "未知参数：$1" >&2; exit 2 ;;
	*) break ;;
	esac
done
if [ $# -ne 2 ]; then
	sed -n '2,13p' "$0" >&2
	exit 2
fi
version="$1"
mkdir -p "$2"
out="$(cd "$2" && pwd)"
root="$(cd "$(dirname "$0")/../.." && pwd)"

fail() { echo "发布构建失败：$*" >&2; exit 1; }
step() { echo "==> $*" >&2; }
sha256() { if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

host="$(go env GOHOSTOS)/$(go env GOHOSTARCH)"
target="${only:-$host}"
[ "$target" = "$host" ] || fail "只能构建本机平台 ${host}（Pi 的依赖要在目标架构上安装），收到 --only $target"
goos="${target%/*}" goarch="${target#*/}"
case "$goarch" in amd64) nodearch=x64 ;; arm64) nodearch=arm64 ;; *) fail "不支持的架构 $goarch" ;; esac
[ "$(node --version)" = "v$NODE_VERSION" ] || fail "构建控制台需要 Node ${NODE_VERSION}，当前是 $(node --version)"

name="loop-now-$version-$goos-$goarch"
bundle="$out/$name"
cache="$out/.cache"
rm -rf "$bundle" "$out/$name.tar.gz" "$out/$name.tar.gz.sha256"
mkdir -p "$bundle" "$cache"

step "构建控制台（同源访问 API：/api/v1）"
(cd "$root/web" && npm ci --no-audit --no-fund && VITE_LOOP_NOW_API_BASE=/api/v1 npm run build)
rm -rf "$root/internal/server/webui/dist"
cp -R "$root/web/dist" "$root/internal/server/webui/dist"

step "构建插件宿主"
(cd "$root/plugins" && npm ci --ignore-scripts --no-audit --no-fund && npm run build)

step "构建 loopctl 发行目录 $version"
rm -rf "$cache/loopctl-dist"
(cd "$root" && go run ./cmd/loopctl-dist -version "$version" -out "$cache/loopctl-dist")

step "构建 loop-server ${version}（$goos/${goarch}，内嵌控制台）"
(cd "$root" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -tags embedweb \
	-ldflags "-s -w -X loop-now/internal/buildinfo.version=$version" -o "$bundle/loop-server" ./cmd/loop-server)

step "组装发行目录"
cp -R "$cache/loopctl-dist" "$bundle/loopctl-dist"
mkdir -p "$bundle/plugins/host"
cp "$root/plugins/host/dist/main.js" "$bundle/plugins/host/main.js"
tar -C "$root/plugins" --exclude node_modules -cf - official | tar -C "$bundle/plugins" -xf -

step "随包 Node $NODE_VERSION"
node_tar="node-v$NODE_VERSION-$goos-$nodearch.tar.gz"
if [ ! -f "$cache/$node_tar" ]; then
	curl -fsSL -o "$cache/$node_tar.part" "https://nodejs.org/dist/v$NODE_VERSION/$node_tar"
	mv "$cache/$node_tar.part" "$cache/$node_tar"
fi
curl -fsSL -o "$cache/SHASUMS256-$NODE_VERSION.txt" "https://nodejs.org/dist/v$NODE_VERSION/SHASUMS256.txt"
want="$(awk -v f="$node_tar" '$2 == f {print $1}' "$cache/SHASUMS256-$NODE_VERSION.txt")"
[ -n "$want" ] && [ "$(sha256 "$cache/$node_tar")" = "$want" ] || fail "$node_tar 的校验和与 nodejs.org 公布的不符"
mkdir -p "$bundle/runtime/node"
tar -xzf "$cache/$node_tar" -C "$bundle/runtime/node" --strip-components=1

step "随包 Pi ${PI_VERSION}（用随包的 Node 与 npm 安装）"
PATH="$bundle/runtime/node/bin:$PATH" npm install --prefix "$bundle/runtime/pi" --omit=dev --no-audit --no-fund \
	"@earendil-works/pi-coding-agent@$PI_VERSION" >&2
pi="$bundle/runtime/pi/node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js"
got="$(PATH="$bundle/runtime/node/bin:/usr/bin:/bin" PI_OFFLINE=1 PI_SKIP_VERSION_CHECK=1 HOME="$cache" "$pi" --version)"
[ "$got" = "$PI_VERSION" ] || fail "随包 Pi 的版本是 ${got}，期望 $PI_VERSION"

if [ "$smoke" = 1 ]; then
	step "用发行目录实际启动一次"
	tmp="$(mktemp -d /tmp/loop-now-smoke.XXXXXX)"
	# 只给数据目录、工作根目录（须在 HOME 之外）、就绪文件与随机端口；Node、Pi、loopctl、插件宿主都取随包的。
	run() {
		exec env -i HOME="$tmp/home" PATH=/usr/bin:/bin LOOP_NOW_DATA_DIR="$tmp/data" LOOP_NOW_WORK_ROOT="$tmp/work" \
			LOOP_NOW_LISTEN_ADDR=127.0.0.1:0 LOOP_NOW_READY_FILE="$1" LOOP_NOW_CONSOLE_ORIGINS=none "$bundle/loop-server"
	}
	mkdir -p "$tmp/home"
	# exec 让后台任务的 PID 就是 loop-server 本身，下面的 TERM 才发给它。
	(exec 2>"$tmp/server.log"; run "$tmp/ready.json") &
	pid=$!
	trap 'kill "$pid" 2>/dev/null || true' EXIT
	for _ in $(seq 1 120); do [ -s "$tmp/ready.json" ] && break; sleep 0.5; done
	[ -s "$tmp/ready.json" ] || { tail -20 "$tmp/server.log" >&2; kill "$pid" 2>/dev/null; fail "服务端没有写出就绪文件"; }
	url="$(sed -E 's/.*"url":"([^"]+)".*/\1/' "$tmp/ready.json")"
	curl -fsS "$url/healthz" | grep -q '"ok":true' || fail "健康检查没有通过"
	curl -fsS "$url/" | grep -qi '<!doctype html' || fail "没有提供内嵌的控制台"
	curl -fsS "$url/api/v1/model-providers" | grep -q "\"piVersion\":\"$PI_VERSION\"" || fail "模型目录不是随包的 Pi $PI_VERSION"
	set +e
	(run "$tmp/ready2.json") 2>/dev/null
	code=$?
	set -e
	[ "$code" = 3 ] || fail "同一数据目录的第二个实例应以 3 退出，实际 $code"
	kill -TERM "$pid"
	wait "$pid" || fail "服务端没有正常退出"
	trap - EXIT
	[ ! -e "$tmp/ready.json" ] || fail "退出后就绪文件应删除"
	rm -rf "$tmp"
fi

step "打包"
tar -czf "$out/$name.tar.gz" -C "$out" "$name"
echo "$(sha256 "$out/$name.tar.gz")  $name.tar.gz" >"$out/$name.tar.gz.sha256"
du -sh "$bundle" "$out/$name.tar.gz" >&2
cat "$out/$name.tar.gz.sha256"
