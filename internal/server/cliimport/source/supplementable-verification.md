# 0.2 source补项投影修正

Main明确准入；仅 `internal/server/cliimport/source/**`。不改正式API/HTTP/store/Tx/现服务。

`Candidate.SupplementableFields []string` 是安全固定字段白名单，与当前必填缺项 `Missing` 分离。支持的DB候选只列原来源为空/缺失的 `database`、`sslMode`、`caCert`，固定顺序；已有字段不列、不能被同值覆盖。不支持/缺秘密/非DB候选为空列表。普通投影与私有entry的slice分开复制，修改返回候选不改变内部事实。

例如MySQL login-path原无database/TLS/CA：Missing=[database,sslMode]，SupplementableFields=[database,sslMode,caCert]。之后选VERIFY_IDENTITY可同时补CA，API不能再把补项限制成Missing子集。源已有verify但缺CA时Missing与SupplementableFields均含CA；原有CA且缺TLS时只可补TLS，原CA保持只读。

SupplementableFields只表示源字段未设置，可由补项输入提供，不保证任意组合有效。例如源DISABLED/disable且无CA时CA仍属源缺失字段，但补CA会被最终renderer拒绝；原TLS不能覆盖。来源无TLS时填verify必须提供有效CA；缺CA/坏PEM/模式组合仍走当前正式模板校验。源secret不能补，原字段即使同值也拒绝覆盖。Missing原判定与Extract业务行为未改变，不增加默认值、秘密补项或新来源能力。

实现：reader.go加字段/安全投影；formats.go新增supplementableDB；pg.go/mysql.go/mysql_tools.go只将各候选真实源字段投影入新列表。新增supplementable_test.go覆盖PG/MySQL各4种模式/CA已有与缺少组合及readonly、无secret不授补项、非DB空列表、非法组合仍拒绝；扩原实际8.4.9 native用例，验证login-path两列表不同，并实际私有提取+补database/VERIFY_IDENTITY/有效CA后正式renderer通过。

定向命令：

```
LOOP_SOURCE_TEST_NETWORK_DENIED=1 GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off /usr/bin/sandbox-exec -p '(version 1)(allow default)(deny network*)' go test -buildvcs=false ./internal/server/cliimport/source -run '^Test(Supplementable|NativeMySQL849)' -count=1 -v
LOOP_SOURCE_TEST_NETWORK_DENIED=1 GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off /usr/bin/sandbox-exec -p '(version 1)(allow default)(deny network*)' go test -buildvcs=false -race ./internal/server/cliimport/source -run '^Test(Supplementable|NativeMySQL849)' -count=1 -v
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go vet ./internal/server/cliimport/source
```

结果：ordinary PASS0.898s，race PASS2.131s，vet exit0；实际native用例0.15/0.24s，无skip/race。只临时合成HOME/CA与固定官方工具，外层network-deny，无真实host/keyring/provider/业务DB或服务操作。未重做其他已完成矩阵，未勾0.2/其他整项；实际HTTP白名单/正式schema由对应owner接，不以本组件代替API集成通过。

已将精确字段语义交watch/UIUX；无仍运行的自持临时操作。

## 本次相关Go输入SHA256

- `reader.go` `bfe5028b54a07670706674624799834374cb80fc7030825f71bbd25072f82e8f`
- `formats.go` `fb7394d35782849a08d936f3940208ce0d8b0a10ef4513ef3dfc0201564a4ace`
- `pg.go` `dcbef5e1f5e8ff79cfc6959f19c45242b7e2c9fb47e3a2c7b203e5ef3c9133b1`
- `mysql.go` `c5baf6333a40ea440ff69d87d8679877354b094e4f9eb3a033308e4fabef1f93`
- `mysql_tools.go` `10b3acf2d3a4528418f5dbce1f46628ef40636cfe5bf018495a3affac0cb2e2a`
- `native_mysql_test.go` `421d18b6429fb865a23f41b9603345f2e25fb15e5d38a9e63f7b92a3c943e57a`
- `supplementable_test.go` `41fbe1c0d4d1a78cf845667a2541a9ed6db1e21f124f2b63a0fcefd903dab4fb`
