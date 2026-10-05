import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { buildExportURL } from './data-export';
import type { ExportDataset } from './data-export';

// ExportAsset 描述选择器所需的本人资产标识与名称。
type ExportAsset = { id: number; name: string; code: string | null };
// DataExportProps 提供已登录资产列表与关闭弹窗回调。
type DataExportProps = { assets: ExportAsset[]; onClose: () => void };
const choices: { key: ExportDataset; title: string; description: string }[] = [
  { key: 'assets', title: '资产清单', description: '当前金额、份额、币种、定投设置和备注' },
  { key: 'prices', title: '股票 / 基金历史行情', description: '每日价格、复权价格或累计净值、货币基金收益' },
  { key: 'returns', title: '历史年化结果', description: '各计算日的 1 / 3 / 5 / 10 年测试结果' },
  { key: 'snapshots', title: '资产每日快照', description: '各资产每天记录的金额、份额和年化' },
  { key: 'rates', title: '汇率历史', description: '所选资产币种的历史人民币汇率' },
];

// DataExport 渲染导出选择窗口，通过只读接口下载单个 CSV 或多文件 ZIP。
export function DataExport({ assets, onClose }: DataExportProps) {
  const [datasets, setDatasets] = useState<ExportDataset[]>(['assets']);
  const [allAssets, setAllAssets] = useState(true);
  const [ids, setIds] = useState<number[]>([]);
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const controller = useRef<AbortController | null>(null);
  const dialog = useRef<HTMLDivElement>(null);

  useEffect(/* 弹窗打开后聚焦关闭按钮，卸载时取消尚未完成的下载。 */ () => {
    const previous = document.activeElement as HTMLElement | null;
    dialog.current?.querySelector<HTMLButtonElement>('button')?.focus();
    return () => { controller.current?.abort(); previous?.focus(); };
  }, []);

  // download 校验选择、处理接口错误，并释放浏览器下载对象。
  async function download(event: FormEvent) {
    event.preventDefault(); setError('');
    try {
      const url = buildExportURL({ datasets, from, to, assetIds: allAssets ? undefined : ids });
      setBusy(true); controller.current = new AbortController();
      const response = await fetch(url, { signal: controller.current.signal });
      if (!response.ok) {
        const result = await response.json().catch(/* 非 JSON 错误仍显示统一的下载失败提示。 */ () => ({}));
        throw new Error(result.error || '导出失败，请重试');
      }
      const blob = await response.blob();
      const filename = response.headers.get('Content-Disposition')?.match(/filename="([^"]+)"/)?.[1] || `asset-data.${datasets.length > 1 ? 'zip' : 'csv'}`;
      const objectURL = URL.createObjectURL(blob);
      const link = document.createElement('a'); link.href = objectURL; link.download = filename;
      document.body.appendChild(link); link.click(); link.remove();
      window.setTimeout(/* 下载触发后释放对象地址，避免反复导出积累内存。 */ () => URL.revokeObjectURL(objectURL), 1000);
    } catch (failure) {
      if (!(failure instanceof DOMException && failure.name === 'AbortError')) setError(failure instanceof Error ? failure.message : '导出失败');
    } finally { setBusy(false); }
  }

  // handleKeys 支持 Escape 关闭并将键盘焦点限制在导出窗口内。
  function handleKeys(event: React.KeyboardEvent) {
    if (event.key === 'Escape') { event.preventDefault(); onClose(); }
    if (event.key !== 'Tab') return;
    const elements = dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled)');
    if (!elements?.length) return;
    const first = elements[0], last = elements[elements.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }

  return <div className="modal-backdrop" onClick={/* 仅点击遮罩空白处关闭弹窗。 */ (event) => { if (event.target === event.currentTarget) onClose(); }}>
    <div className="modal export-modal" role="dialog" aria-modal="true" aria-labelledby="export-title" ref={dialog} onKeyDown={handleKeys}>
      <button className="modal-close" type="button" aria-label="关闭数据导出" onClick={onClose}>×</button>
      <h2 id="export-title">数据导出</h2>
      <p>选择需要的数据。单类下载 CSV，多类打包为 ZIP，可用表格软件打开。</p>
      <form className="export-form" onSubmit={download}>
        <fieldset disabled={busy}><legend>导出内容</legend>
          {choices.map(/* 为每个数据类别渲染可选项和内容说明。 */ choice => <label className="export-choice" key={choice.key}>
            <input type="checkbox" checked={datasets.includes(choice.key)} onChange={/* 切换当前数据类别。 */ event => setDatasets(event.target.checked ? [...datasets, choice.key] : datasets.filter(/* 排除取消选择的类别。 */ key => key !== choice.key))} />
            <span><strong>{choice.title}</strong><small>{choice.description}</small></span>
          </label>)}
        </fieldset>
        <fieldset disabled={busy}><legend>资产范围</legend>
          <label className="export-choice"><input type="checkbox" checked={allAssets} onChange={/* 切换全部资产与指定资产范围。 */ event => setAllAssets(event.target.checked)} /><span>全部当前资产（{assets.length} 个）</span></label>
          {!allAssets && <div className="export-assets">{assets.map(/* 展示当前账号的可选持仓。 */ asset => <label className="export-choice" key={asset.id}>
            <input type="checkbox" checked={ids.includes(asset.id)} onChange={/* 切换该持仓的导出选择。 */ event => setIds(event.target.checked ? [...ids, asset.id] : ids.filter(/* 排除取消选择的资产编号。 */ id => id !== asset.id))} />
            <span>{asset.name}{asset.code ? ` · ${asset.code}` : ''}</span>
          </label>)}</div>}
        </fieldset>
        <div className="form-two export-dates">
          <label>开始日期<input type="date" value={from} max={to || undefined} disabled={busy} onChange={/* 更新开始日期。 */ event => setFrom(event.target.value)} /></label>
          <label>结束日期<input type="date" value={to} min={from || undefined} disabled={busy} onChange={/* 更新结束日期。 */ event => setTo(event.target.value)} /></label>
        </div>
        <p className="export-hint">日期留空表示全部已缓存历史，范围包含起止日期；资产清单始终导出当前状态。行情不足时仅导出现有记录，空结果保留表头。</p>
        {error && <p className="export-error" role="alert">{error}</p>}
        <button className="primary-button" type="submit" disabled={busy || !datasets.length || (!allAssets && !ids.length)}>{busy ? '正在生成文件…' : '下载所选数据'}</button>
      </form>
    </div>
  </div>;
}
