package app

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/cliimport"
	"loop-now/internal/server/cliimport/source"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/config"
	"loop-now/internal/server/store"
)

// configureCLIImport never opens the source HOME during construction or recovery.
// A factory is a trusted Go fixture dependency, never an HTTP/configurable path.
func (a *App) configureCLIImport(ctx context.Context, cfg *config.Config, o Options, catalog *clitemplate.Catalog, workRoot string) error {
	// A lower explicit TEST ceiling deliberately excludes the component entirely.
	// Production always migrates all and never catches a missing-table error.
	if o.MigrationTarget > 0 && o.MigrationTarget < 17 {
		return nil
	}
	hasher := importHasher{}
	var e error
	factory := o.CLIImportReaderFactory
	if factory == nil {
		u, e := user.LookupId(strconv.Itoa(os.Getuid()))
		if e != nil || u == nil || !filepath.IsAbs(u.HomeDir) {
			return errors.New("本机导入来源用户不可确定")
		}
		root := filepath.Clean(u.HomeDir)
		factory = func(ctx context.Context) (cliimport.Reader, error) {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			r, e := source.New(root, catalog)
			if e != nil {
				return nil, e
			}
			configureImportMySQLTools(ctx, r, root, workRoot, catalog)
			return r, nil
		}
	}
	a.CLIImport, e = cliimport.NewService(a.Store, catalog, cliimport.Options{InstanceID: a.InstanceID, ReaderFactory: factory, Hasher: hasher, ScanTTL: cfg.CLIImportScanTTL, OperationTimeout: cfg.CLIImportTimeout, RecordTimeout: cfg.CLIImportRecordTimeout, MaxBatch: cfg.CLIImportMaxBatch, MaxActive: cfg.CLIImportMaxActive, MaxScansPerProject: cfg.CLIImportMaxScans, MaxCredential: cfg.MaxCredential, Changed: func(ctx context.Context, project uuid.UUID) {
		agents, e := a.Store.ListAgents(ctx, store.ProjectScope(project))
		if e != nil {
			return
		}
		for _, agent := range agents {
			if a.Config.AgentChanged != nil {
				a.Config.AgentChanged(agent.ID)
			}
		}
	}})
	if e != nil {
		return e
	}
	if e = a.CLIImport.Recover(ctx); e != nil {
		return e
	}
	a.Store.CLIImportDeletingTx = a.Store.TerminateProjectCLIImportsTx
	a.Store.CLIImportDeleted = func(project uuid.UUID) {
		timeout := cfg.CLIImportRecordTimeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if a.CLIImport.CancelProject(ctx, project) != nil {
			a.Log.Warn("cli_import_cancel_drain_timeout")
		}
	}
	return nil
}
