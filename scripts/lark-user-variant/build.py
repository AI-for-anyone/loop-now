#!/usr/bin/env python3
"""发行阶段构建；不是运行机 installer。只使用固定官方源码、仅 authsidecar tag。"""
import argparse, gzip, hashlib, io, json, os, pathlib, subprocess, tarfile, urllib.request
COMMIT = "f065bf5b645af381f9b7475ce721451e6ca36a23"
SOURCE_SHA = "997aaddb0e74cc17903ee463c9bd6554ee671d7bdd5e0aa58343e74219ee46dd"
VERSION = "lark-cli-user-1.0.94-loop-authsidecar.1"
URL = f"https://codeload.github.com/larksuite/cli/tar.gz/{COMMIT}"
def sha(p):
    return hashlib.file_digest(open(p, "rb"), "sha256").hexdigest()
def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--archive", type=pathlib.Path)
    ap.add_argument("--out", type=pathlib.Path, required=True)
    a = ap.parse_args()
    out = a.out.resolve(); out.mkdir(parents=True, exist_ok=False)
    archive = a.archive.resolve() if a.archive else out / "official-source.tar.gz"
    if not a.archive:
        with urllib.request.urlopen(URL, timeout=45) as r, open(archive, "xb") as w:
            while chunk := r.read(1024*1024): w.write(chunk)
    if sha(archive) != SOURCE_SHA: raise SystemExit("source digest mismatch")
    src = out / "source"; src.mkdir()
    with tarfile.open(archive) as t:
        for m in t.getmembers():
            p = pathlib.PurePosixPath(m.name)
            if p.is_absolute() or ".." in p.parts or not (m.isdir() or m.isfile()):
                raise SystemExit("unsafe archive member")
        t.extractall(src, filter="data")
    root = src / ("cli-" + COMMIT)
    env = {**os.environ, "CGO_ENABLED":"0", "GOTOOLCHAIN":"local", "GOFLAGS":"", "GOWORK":"off"}
    target = subprocess.check_output(["go","env","GOOS","GOARCH"], env=env, text=True).split()
    if target != ["darwin","arm64"]: raise SystemExit("unverified build platform")
    toolchain = subprocess.check_output(["go","version"],text=True).strip()
    if toolchain != "go version go1.27.1 darwin/arm64": raise SystemExit("unverified toolchain")
    binary = out / "lark-cli"
    ldflags = "-X github.com/larksuite/cli/internal/build.Version=1.0.94-loop-authsidecar.1 -X github.com/larksuite/cli/internal/build.Date=platform-controlled"
    subprocess.run(["go","build","-buildvcs=false","-mod=readonly","-trimpath","-tags=authsidecar", "-ldflags",ldflags, "-o",str(binary),"."],cwd=root,env=env,check=True,timeout=300)
    buildinfo = subprocess.check_output(["go","version","-m",str(binary)],text=True)
    if "-tags=authsidecar" not in buildinfo: raise SystemExit("tag not recorded")
    repo = pathlib.Path(__file__).resolve().parents[2]
    probe = out / "lark-cli-sandbox-probe"
    subprocess.run(["go","build","-buildvcs=false","-mod=readonly","-trimpath","-o",str(probe),"./scripts/lark-user-variant/sandbox-probe"],cwd=repo,env=env,check=True,timeout=120)
    launcher = out / "lark-cli-launcher"
    subprocess.run(["go","build","-buildvcs=false","-mod=readonly","-trimpath","-o",str(launcher),"./scripts/lark-user-variant/launcher"],cwd=repo,env=env,check=True,timeout=120)
    (out/"bin").mkdir()
    (out/"bin/lark-cli").write_bytes(launcher.read_bytes())
    (out/"bin/lark-cli").chmod(0o755)
    broker_sources = {p.name: sha(p) for p in sorted((repo/"internal/server/larksidecar").glob("*.go")) if not p.name.endswith("_test.go")}
    manifest = {"sandboxProbe": {"file":"lark-cli-sandbox-probe","sha256":sha(probe),"protocol":"loop-now-lark-sandbox-probe-v2","sourceSHA256":sha(repo/"scripts/lark-user-variant/sandbox-probe/main.go")},"platformBrokerSourceSHA256":broker_sources,"buildScriptSHA256":sha(pathlib.Path(__file__).resolve()),"ldflags":ldflags,"artifactVersion":VERSION,"officialRelease":False,"sourceURL":URL,"sourceCommit":COMMIT,"sourceSHA256":SOURCE_SHA,"sourceModified":False,"buildTags":["authsidecar"],"platform":"darwin-arm64","toolchain":subprocess.check_output(["go","version"],text=True).strip(),"binarySHA256":sha(binary),"goModSHA256":sha(root/"go.mod"),"goSumSHA256":sha(root/"go.sum"),"buildInfo":buildinfo}
    manifest["launcher"] = {"file":"lark-cli-launcher", "entryFile":"bin/lark-cli", "sha256":sha(launcher), "protocol":"loop-now-lark-exec-v1", "environment":["LOOP_NOW_LARK_EXEC_URL","LOOP_NOW_LARK_EXEC_KEY"], "sourceSHA256":sha(repo/"scripts/lark-user-variant/launcher/main.go")}
    (out/"manifest.json").write_text(json.dumps(manifest,indent=2)+"\n")
    package = out / (VERSION + "-darwin-arm64.tar.gz")
    with open(package,"xb") as raw, gzip.GzipFile(filename="",mode="wb",fileobj=raw,mtime=0) as gz, tarfile.open(fileobj=gz,mode="w") as tar:
        entries = [("lark-cli",binary.read_bytes(),0o755),("lark-cli-sandbox-probe",probe.read_bytes(),0o755),("lark-cli-launcher",launcher.read_bytes(),0o755),("bin/lark-cli",launcher.read_bytes(),0o755),("manifest.json",(out/"manifest.json").read_bytes(),0o644),("LICENSE",(root/"LICENSE").read_bytes(),0o644)]
        for name,data,mode in entries:
            info=tarfile.TarInfo(name);info.size=len(data);info.mode=mode;info.mtime=0;tar.addfile(info,io.BytesIO(data))
    (out/"package-sha256.json").write_text(json.dumps({"package":package.name,"sha256":sha(package),"publisher":"Loop Now platform controlled build; not official release"},indent=2)+"\n")
    print(json.dumps({k:v for k,v in manifest.items() if k != "buildInfo"},indent=2))
if __name__ == "__main__": main()
