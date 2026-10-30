// Package modelconfig 管理原生 models.json 文档，并通过固定 Pi 校验后原子发布目录。
package modelconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/store"
)

const DefaultModelsJSON = "{\"providers\":{}}"

type Settings struct {
	ModelsJSON string `json:"modelsJson"`
	Revision   int64  `json:"revision"`
}

// Store 是设置服务需要的窄持久化接口；生产实现是 store.Store。
type Store interface {
	GetModelSettings(context.Context) (store.ModelSettings, error)
	CompareAndSwapModelSettings(context.Context, string, int64) (store.ModelSettings, error)
}

type Service struct {
	Store   Store
	Models  *modelcatalog.Registry
	Options modelcatalog.Options
	mu      sync.Mutex
}

// Reload 从数据库重建当前目录；只有 Pi 完整接受配置后才替换共享快照。
func (s *Service) Reload(ctx context.Context) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.Store.GetModelSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	catalog, err := s.load(ctx, row.ModelsJSON, row.Revision)
	if err != nil {
		return Settings{}, err
	}
	s.Models.Replace(catalog)
	return Settings{ModelsJSON: row.ModelsJSON, Revision: row.Revision}, nil
}

func (s *Service) Get(ctx context.Context) (Settings, error) {
	row, err := s.Store.GetModelSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	return Settings{ModelsJSON: row.ModelsJSON, Revision: row.Revision}, nil
}

func (s *Service) Put(ctx context.Context, modelsJSON string, expected int64) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.Store.GetModelSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	if current.Revision != expected {
		return Settings{}, apperr.PreconditionFailed("模型配置已被修改，请重新读取后再保存")
	}
	catalog, err := s.load(ctx, modelsJSON, current.Revision+1)
	if err != nil {
		return Settings{}, apperr.Invalid("modelsJson", err.Error())
	}
	row, err := s.Store.CompareAndSwapModelSettings(ctx, modelsJSON, expected)
	if err != nil {
		return Settings{}, err
	}
	s.Models.Replace(catalog)
	return Settings{ModelsJSON: row.ModelsJSON, Revision: row.Revision}, nil
}

func (s *Service) load(ctx context.Context, text string, revision int64) (*modelcatalog.Catalog, error) {
	if !json.Valid([]byte(text)) {
		return nil, fmt.Errorf("models.json 必须是合法 JSON")
	}
	o := s.Options
	o.ModelsJSON, o.Revision = []byte(text), revision
	return modelcatalog.Load(ctx, o)
}
