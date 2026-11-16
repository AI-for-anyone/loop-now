#!/usr/bin/env bash
# Release build: produces the loop-now release directory and archive for this machine's platform, and writes the
# checksum. The release directory contains:
#   loop-server               the server, with the console embedded
#   loopctl-dist/<version>/   loopctl for every platform, with its SHA256SUMS and manifest.json
#   plugins/host/main.js      the plugin host; plugins/official/ holds the official plugins
#   runtime/node/             the official Node (with npm); runtime/pi/ is the Pi installed with it
# Unless configured otherwise, loop-server uses these bundled files (see Bundled* in internal/server/config), so it
# runs as soon as the archive is unpacked.
#
#   scripts/release/build.sh [--only <os>/<arch>] [--smoke] <version> <output directory>
#
# Pi's dependencies must be installed on the target architecture, so only this machine's platform is built; --only
# confirms that the target is this machine. --smoke starts the result once.
# Needs: Go, Node 24.21.0 (to build the console and the plugin host), git, curl, and network access (to download Node
# and install the dependencies and Pi).
set -euo pipefail

NODE_VERSION=24.21.0
PI_VERSION=1.4.0

only="" smoke=0
while [ $# -gt 0 ]; do
	case "$1" in
	--only) only="$2"; shift 2 ;;
	--smoke) smoke=1; shift ;;
	-*) echo "Unknown option: $1" >&2; exit 2 ;;
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

fail() { echo "Release build failed: $*" >&2; exit 1; }
step() { echo "==> $*" >&2; }
sha256() { if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

host="$(go env GOHOSTOS)/$(go env GOHOSTARCH)"
target="${only:-$host}"
[ "$target" = "$host" ] || fail "Only this machine's platform ${host} can be built (Pi's dependencies must be installed on the target architecture); got --only $target."
goos="${target%/*}" goarch="${target#*/}"
case "$goarch" in amd64) nodearch=x64 ;; arm64) nodearch=arm64 ;; *) fail "Unsupported architecture $goarch." ;; esac
[ "$(node --version)" = "v$NODE_VERSION" ] || fail "Building the console needs Node ${NODE_VERSION}; found $(node --version)."

name="loop-now-$version-$goos-$goarch"
bundle="$out/$name"
cache="$out/.cache"
rm -rf "$bundle" "$out/$name.tar.gz" "$out/$name.tar.gz.sha256"
mkdir -p "$bundle" "$cache"

step "Build the console (same-origin API: /api/v1)"
(cd "$root/web" && npm ci --no-audit --no-fund && VITE_LOOP_NOW_API_BASE=/api/v1 npm run build)
rm -rf "$root/internal/server/webui/dist"
cp -R "$root/web/dist" "$root/internal/server/webui/dist"

step "Build the plugin host"
(cd "$root/plugins" && npm ci --ignore-scripts --no-audit --no-fund && npm run build)

step "Build the loopctl release directory $version"
rm -rf "$cache/loopctl-dist"
(cd "$root" && go run ./cmd/loopctl-dist -version "$version" -out "$cache/loopctl-dist")

step "Build loop-server ${version} ($goos/${goarch}, console embedded)"
(cd "$root" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -tags embedweb \
	-ldflags "-s -w -X loop-now/internal/buildinfo.version=$version" -o "$bundle/loop-server" ./cmd/loop-server)

step "Assemble the release directory"
cp -R "$cache/loopctl-dist" "$bundle/loopctl-dist"
mkdir -p "$bundle/plugins/host"
cp "$root/plugins/host/dist/main.js" "$bundle/plugins/host/main.js"
tar -C "$root/plugins" --exclude node_modules -cf - official | tar -C "$bundle/plugins" -xf -

step "Bundle Node $NODE_VERSION"
node_tar="node-v$NODE_VERSION-$goos-$nodearch.tar.gz"
if [ ! -f "$cache/$node_tar" ]; then
	curl -fsSL -o "$cache/$node_tar.part" "https://nodejs.org/dist/v$NODE_VERSION/$node_tar"
	mv "$cache/$node_tar.part" "$cache/$node_tar"
fi
curl -fsSL -o "$cache/SHASUMS256-$NODE_VERSION.txt" "https://nodejs.org/dist/v$NODE_VERSION/SHASUMS256.txt"
want="$(awk -v f="$node_tar" '$2 == f {print $1}' "$cache/SHASUMS256-$NODE_VERSION.txt")"
[ -n "$want" ] && [ "$(sha256 "$cache/$node_tar")" = "$want" ] || fail "The checksum of $node_tar does not match the one published on nodejs.org."
mkdir -p "$bundle/runtime/node"
tar -xzf "$cache/$node_tar" -C "$bundle/runtime/node" --strip-components=1

step "Bundle Pi ${PI_VERSION} (installed with the bundled Node and npm)"
PATH="$bundle/runtime/node/bin:$PATH" npm install --prefix "$bundle/runtime/pi" --omit=dev --no-audit --no-fund \
	"@earendil-works/pi-coding-agent@$PI_VERSION" >&2
pi="$bundle/runtime/pi/node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js"
got="$(PATH="$bundle/runtime/node/bin:/usr/bin:/bin" PI_OFFLINE=1 PI_SKIP_VERSION_CHECK=1 HOME="$cache" "$pi" --version)"
[ "$got" = "$PI_VERSION" ] || fail "The bundled Pi is version ${got}; expected $PI_VERSION."

if [ "$smoke" = 1 ]; then
	step "Start the release directory once"
	tmp="$(mktemp -d /tmp/loop-now-smoke.XXXXXX)"
	# Only the data directory, the work root (must be outside HOME), the ready file and a random port are given; Node,
	# Pi, loopctl and the plugin host all come from the bundle.
	run() {
		exec env -i HOME="$tmp/home" PATH=/usr/bin:/bin LOOP_NOW_DATA_DIR="$tmp/data" LOOP_NOW_WORK_ROOT="$tmp/work" \
			LOOP_NOW_LISTEN_ADDR=127.0.0.1:0 LOOP_NOW_READY_FILE="$1" LOOP_NOW_CONSOLE_ORIGINS=none "$bundle/loop-server"
	}
	mkdir -p "$tmp/home"
	# exec makes the background job's PID loop-server itself, so the TERM below reaches it.
	(exec 2>"$tmp/server.log"; run "$tmp/ready.json") &
	pid=$!
	trap 'kill "$pid" 2>/dev/null || true' EXIT
	for _ in $(seq 1 120); do [ -s "$tmp/ready.json" ] && break; sleep 0.5; done
	[ -s "$tmp/ready.json" ] || { tail -20 "$tmp/server.log" >&2; kill "$pid" 2>/dev/null; fail "The server did not write the ready file."; }
	url="$(sed -E 's/.*"url":"([^"]+)".*/\1/' "$tmp/ready.json")"
	curl -fsS "$url/healthz" | grep -q '"ok":true' || fail "The health check failed."
	curl -fsS "$url/" | grep -qi '<!doctype html' || fail "The embedded console is not served."
	curl -fsS "$url/api/v1/model-providers" | grep -q "\"piVersion\":\"$PI_VERSION\"" || fail "The model catalog is not from the bundled Pi $PI_VERSION."
	# The login helper and the legacy Anthropic extension (the copies the server just installed into the work root)
	# load the bundled Pi's providers with the bundled Node; pi-ai is located by the server's preflight rule: inside
	# pi-coding-agent, otherwise next to it (npm --prefix hoists it).
	assets="$(find "$tmp/work/platform/auth-helper" -mindepth 1 -maxdepth 1 -type d ! -name '.*')"
	pkg="$bundle/runtime/pi/node_modules/@earendil-works/pi-coding-agent"
	ai="$pkg/node_modules/@earendil-works/pi-ai"
	[ -d "$ai" ] || ai="$(dirname "$pkg")/pi-ai"
	env -i PATH=/usr/bin:/bin LOOP_NOW_AUTH_PI_VERSION="$PI_VERSION" LOOP_NOW_PI_PACKAGE_ROOT="$pkg" LOOP_NOW_PI_AI_DIR="$ai" \
		"$bundle/runtime/node/bin/node" --input-type=module -e '
import { pathToFileURL } from "node:url";
const [assets, pkg, ai] = process.argv.slice(1);
const { loadPiProvider } = await import(pathToFileURL(assets + "/pi-provider.mjs").href);
await loadPiProvider(pkg, ai);
let registered;
await (await import(pathToFileURL(assets + "/legacy-anthropic.mjs").href)).default({ registerProvider: p => { registered = p; } });
if (registered?.id !== "anthropic") throw new Error("anthropic provider not registered");
' "$assets" "$pkg" "$ai" || fail "The login helper cannot load the bundled Pi's providers."
	set +e
	(run "$tmp/ready2.json") 2>/dev/null
	code=$?
	set -e
	[ "$code" = 3 ] || fail "A second instance on the same data directory should exit with 3; it exited with $code."
	kill -TERM "$pid"
	wait "$pid" || fail "The server did not exit cleanly."
	trap - EXIT
	[ ! -e "$tmp/ready.json" ] || fail "The ready file should be deleted after exit."
	rm -rf "$tmp"
fi

step "Package"
tar -czf "$out/$name.tar.gz" -C "$out" "$name"
echo "$(sha256 "$out/$name.tar.gz")  $name.tar.gz" >"$out/$name.tar.gz.sha256"
du -sh "$bundle" "$out/$name.tar.gz" >&2
cat "$out/$name.tar.gz.sha256"
