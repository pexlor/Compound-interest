// ExportDataset 列出服务端支持的可选导出数据类别。
export type ExportDataset = 'assets' | 'prices' | 'returns' | 'snapshots' | 'rates';
// ExportSelection 描述用户的导出范围；assetIds 未指定表示全部当前资产。
export type ExportSelection = {
  datasets: ExportDataset[];
  from?: string;
  to?: string;
  assetIds?: number[];
};

// buildExportURL 验证用户选择并构建只读下载地址，日期包含两端。
export function buildExportURL(selection: ExportSelection): string {
  if (!selection.datasets.length) throw new Error('至少选择一种导出数据');
  if (selection.assetIds && !selection.assetIds.length) throw new Error('至少选择一个资产');
  for (const date of [selection.from, selection.to]) {
    if (date && (!/^\d{4}-\d{2}-\d{2}$/.test(date) || !Number.isFinite(Date.parse(date)) || new Date(date).toISOString().slice(0, 10) !== date)) {
      throw new Error('请选择有效的日期');
    }
  }
  if (selection.from && selection.to && selection.from > selection.to) throw new Error('开始日期不能晚于结束日期');
  const query = new URLSearchParams({ datasets: selection.datasets.join(',') });
  if (selection.from) query.set('from', selection.from);
  if (selection.to) query.set('to', selection.to);
  if (selection.assetIds) query.set('assetIds', selection.assetIds.join(','));
  return `/api/export?${query.toString()}`;
}
