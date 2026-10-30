// Package modelconfig 管理原生 models.json 文档，并通过固定 Pi 校验后原子发布目录。
package modelconfig

import (
	"context"
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
	LoadError  string `json:"loadError,omitempty"`
}

const fallbackLoadError = "保存的 models.json 未通过当前 Pi 校验，已临时使用内置模型目录；请修正后重新保存。"

// Store 是设置服务需要的窄持久化接口；生产实现是 store.Store。
type Store interface {
	GetModelSettings(context.Context) (store.ModelSettings, error)
	CompareAndSwapModelSettings(context.Context, string, int64) (store.ModelSettings, error)
}

type Service struct {
	Store     Store
	Models    *modelcatalog.Registry
	Options   modelcatalog.Options
	mu        sync.Mutex
	loadError string
}

// Reload 从数据库重建当前目录。用户配置失效时保留原文供修复，但先退回内置目录，
// 避免 Pi 升级或 schema 变化让控制台本身无法启动。
func (s *Service) Reload(ctx context.Context) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.Store.GetModelSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	settings := Settings{ModelsJSON: row.ModelsJSON, Revision: row.Revision}
	catalog, loadErr := s.load(ctx, row.ModelsJSON, row.Revision)
	if loadErr != nil {
		catalog, err = modelcatalog.Load(ctx, s.Options)
		if err != nil {
			return Settings{}, fmt.Errorf("回退内置模型目录失败：%w", err)
		}
		settings.LoadError = fallbackLoadError
	}
	s.Models.Replace(catalog)
	s.loadError = settings.LoadError
	return settings, nil
}

func (s *Service) Get(ctx context.Context) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.Store.GetModelSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	return Settings{ModelsJSON: row.ModelsJSON, Revision: row.Revision, LoadError: s.loadError}, nil
}

func (s *Service) Put(ctx context.Context, modelsJSON string, expected int64) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected < 1 {
		return Settings{}, apperr.Invalid("expectedRevision", "必须提供正整数版本")
	}
	current, err := s.Store.GetModelSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	if current.Revision != expected {
		return Settings{}, apperr.PreconditionFailed("模型配置已被修改，请重新读取后再保存")
	}
	catalog, err := s.load(ctx, modelsJSON, current.Revision+1)
	if err != nil {
		message, safe := validationMessage(err)
		if !safe {
			message = "配置未通过 Pi 校验，请检查 Provider、模型与字段值"
		}
		return Settings{}, apperr.Invalid("modelsJson", message)
	}
	row, err := s.Store.CompareAndSwapModelSettings(ctx, modelsJSON, expected)
	if err != nil {
		return Settings{}, err
	}
	s.Models.Replace(catalog)
	s.loadError = ""
	return Settings{ModelsJSON: row.ModelsJSON, Revision: row.Revision}, nil
}

func (s *Service) load(ctx context.Context, text string, revision int64) (*modelcatalog.Catalog, error) {
	if err := validateModelsJSON(text); err != nil {
		return nil, err
	}
	o := s.Options
	o.ModelsJSON, o.Revision = []byte(text), revision
	return modelcatalog.Load(ctx, o)
}
