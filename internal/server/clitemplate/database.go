package clitemplate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/dbcli"
	"sort"
)

type DatabaseEntry struct {
	Connection dbcli.Connection
	Payload    Payload
}

func renderDatabase(cli string, values map[string]string) ([]PayloadFile, error) {
	f, e := dbcli.ParseFields(cli, values)
	if e != nil {
		return nil, apperr.Invalid("fields", "数据库连接字段或TLS配置无效")
	}
	b, e := json.Marshal(f)
	if e != nil {
		return nil, errors.New("数据库配置编码失败")
	}
	return []PayloadFile{{Path: dbcli.InputPath(cli), Content: b}}, nil
}

// AggregateDatabase rewrites per-account relative paths into one scope payload;
// every source is validated before any execution files are materialized.
func AggregateDatabase(cli string, entries []DatabaseEntry) (Payload, error) {
	out := Payload{Env: map[string]string{}, Files: []PayloadFile{}}
	connections := make([]dbcli.Connection, 0, len(entries))
	for _, entry := range entries {
		c := entry.Connection
		if c.AccountID == [16]byte{} || c.VersionID == [16]byte{} || len(entry.Payload.Env) != 0 || len(entry.Payload.Files) != 1 || entry.Payload.Files[0].Path != dbcli.InputPath(cli) {
			return out, errors.New("数据库捕获配置无效")
		}
		decoder := json.NewDecoder(bytes.NewReader(entry.Payload.Files[0].Content))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&c.Fields) != nil || decoder.Decode(new(any)) != io.EOF || dbcli.Validate(cli, c.Fields) != nil {
			return out, errors.New("数据库捕获配置无效")
		}
		connections = append(connections, c)
	}
	sort.Slice(connections, func(i, j int) bool { return connections[i].AccountID.String() < connections[j].AccountID.String() })
	for j := 1; j < len(connections); j++ {
		if connections[j-1].AccountID == connections[j].AccountID {
			return out, errors.New("数据库捕获ID重复")
		}
	}
	b, e := json.Marshal(connections)
	if e != nil {
		return out, e
	}
	out.Files = append(out.Files, PayloadFile{Path: dbcli.ConfigPath(cli), Content: b})
	return out, nil
}
