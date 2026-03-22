package sync

import (
	"context"

	"github.com/urusaqqrun/vault-mirror-service/mirror"
)

// FullExportBootstrapper 使用 full export 初始化 owner 的 Vault。
type FullExportBootstrapper struct {
	fs     mirror.VaultFS
	reader FullExporter
}

func NewFullExportBootstrapper(fs mirror.VaultFS, reader FullExporter) *FullExportBootstrapper {
	return &FullExportBootstrapper{
		fs:     fs,
		reader: reader,
	}
}

func (b *FullExportBootstrapper) BootstrapOwner(ctx context.Context, ownerUserID string) error {
	return ExportFullVault(ctx, b.fs, b.reader, ownerUserID)
}
