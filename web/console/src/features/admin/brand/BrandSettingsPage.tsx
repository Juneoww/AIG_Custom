/**
 * 功能：让管理员维护产品名、主题色、水印和受限的品牌 Logo。
 * 实现：浏览器先验证 PNG/JPEG、1MiB、尺寸与像素，再以 Base64 提交给服务端进行最终校验。
 * 输入：管理员身份、品牌配置、用户选择的本地图片文件和表单字段。
 * 输出：安全 Logo 预览、固定错误提示及单次受 CSRF 保护的更新请求。
 * 依赖：Fluent UI、TanStack Query、管理 API 和共享状态组件。
 */
import { Button, Input, MessageBar, MessageBarBody, makeStyles, tokens } from '@fluentui/react-components'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'

import { ApiError } from '../../../shared/api/errors'
import { PageHeader } from '../../../shared/components/PageHeader'
import { StatePanel } from '../../../shared/components/StatePanel'
import { fetchBrandConfig, updateBrandConfig, type BrandConfigView } from '../api'

const MAX_LOGO_BYTES = 1024 * 1024
const MAX_DIMENSION = 4096
const MAX_PIXELS = 16_000_000

const useStyles = makeStyles({
  page: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL },
  form: { display: 'grid', gap: tokens.spacingVerticalS, maxWidth: '42rem' },
  preview: { maxWidth: '12rem', maxHeight: '8rem', objectFit: 'contain', border: `1px solid ${tokens.colorNeutralStroke2}` },
  actions: { display: 'flex', gap: tokens.spacingHorizontalS, flexWrap: 'wrap' },
})

interface Draft {
  product_name: string
  primary_color: string
  watermark: string
  logo: string
  logo_mime: BrandConfigView['logo_mime']
}

function draftFrom(config: BrandConfigView): Draft {
  return {
    product_name: config.product_name,
    primary_color: config.primary_color,
    watermark: config.watermark,
    logo: config.logo,
    logo_mime: config.logo_mime,
  }
}

function dataURLFor(logo: string, mime: Draft['logo_mime']): string {
  return logo && mime ? `data:${mime};base64,${logo}` : ''
}

function byteBase64(bytes: Uint8Array): string {
  let text = ''
  for (let index = 0; index < bytes.length; index += 0x8000) text += String.fromCharCode(...bytes.subarray(index, index + 0x8000))
  return btoa(text)
}

async function validateLogo(file: File): Promise<{ logo: string; logo_mime: 'image/png' | 'image/jpeg' }> {
  const mime = file.type === 'image/png' ? 'image/png' : file.type === 'image/jpeg' ? 'image/jpeg' : undefined
  if (!mime) throw new Error('仅支持 PNG 或 JPEG 图片。')
  if (file.size <= 0 || file.size > MAX_LOGO_BYTES) throw new Error('Logo 文件必须小于或等于 1 MiB。')
  const bytes = new Uint8Array(await file.arrayBuffer())
  const logo = byteBase64(bytes)
  const dataURL = `data:${mime};base64,${logo}`
  await new Promise<void>((resolve, reject) => {
    const image = new Image()
    image.onload = () => {
      if (image.naturalWidth <= 0 || image.naturalHeight <= 0 || image.naturalWidth > MAX_DIMENSION || image.naturalHeight > MAX_DIMENSION ||
        image.naturalWidth * image.naturalHeight > MAX_PIXELS) {
        reject(new Error('Logo 图片尺寸或像素超出限制。'))
        return
      }
      resolve()
    }
    image.onerror = () => reject(new Error('无法解析 Logo 图片。'))
    image.src = dataURL
  })
  return { logo, logo_mime: mime }
}

export function BrandSettingsPage() {
  const styles = useStyles()
  const client = useQueryClient()
  const [draft, setDraft] = useState<Draft | null>(null)
  const [notice, setNotice] = useState('')
  const [saving, setSaving] = useState(false)
  const fileRef = useRef<HTMLInputElement | null>(null)
  const query = useQuery({ queryKey: ['brand-settings'], queryFn: ({ signal }) => fetchBrandConfig(signal), retry: false })
  const value = draft ?? (query.data ? draftFrom(query.data) : null)

  const update = (next: Partial<Draft>) => setDraft((current) => ({ ...(current ?? draftFrom(query.data!)), ...next }))

  const selectLogo = async (file?: File) => {
    if (!file) return
    setNotice('')
    try {
      update(await validateLogo(file))
    } catch (error) {
      setNotice(error instanceof Error ? error.message : '无法验证 Logo 图片。')
      if (fileRef.current) fileRef.current.value = ''
    }
  }

  const save = async () => {
    if (!value || saving || !/^#[0-9A-Fa-f]{6}$/.test(value.primary_color) || !value.product_name.trim()) {
      setNotice('请填写有效的产品名称和六位主题色。')
      return
    }
    setSaving(true)
    setNotice('')
    try {
      const saved = await updateBrandConfig({ ...value, product_name: value.product_name.trim(), watermark: value.watermark.trim() })
      setDraft(draftFrom(saved))
      await client.invalidateQueries({ queryKey: ['brand-settings'] })
      setNotice('品牌设置已保存。')
    } catch {
      setNotice('品牌设置保存失败，请显式重试。')
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className={styles.page}>
      <PageHeader title="品牌设置" description="仅管理员可更新产品标识。浏览器和服务端都会校验 PNG/JPEG、1 MiB 与像素限制。" />
      {query.isPending ? <StatePanel state="loading" title="正在加载品牌设置" /> : null}
      {query.isError && query.error instanceof ApiError && query.error.kind === 'forbidden' ? <StatePanel state="forbidden" title="无权管理品牌设置" /> : null}
      {query.isError && !(query.error instanceof ApiError && query.error.kind === 'forbidden') ? <StatePanel state="error" title="暂时无法加载品牌设置" actionLabel="重试" onAction={() => void query.refetch()} /> : null}
      {value ? (
        <form className={styles.form} onSubmit={(event) => { event.preventDefault(); void save() }}>
          <label>产品名称<Input aria-label="产品名称" maxLength={128} value={value.product_name} onChange={(_, data) => update({ product_name: data.value })} /></label>
          <label>主题色<Input aria-label="主题色" value={value.primary_color} inputMode="text" maxLength={7} onChange={(_, data) => update({ primary_color: data.value })} /></label>
          <label>水印<Input aria-label="水印" maxLength={64} value={value.watermark} onChange={(_, data) => update({ watermark: data.value })} /></label>
          <label>品牌 Logo 文件<input ref={fileRef} aria-label="品牌 Logo 文件" type="file" accept="image/png,image/jpeg" onChange={(event) => void selectLogo(event.currentTarget.files?.[0])} /></label>
          {dataURLFor(value.logo, value.logo_mime) ? <img className={styles.preview} src={dataURLFor(value.logo, value.logo_mime)} alt="品牌 Logo 预览" /> : null}
          <div className={styles.actions}>
            <Button appearance="secondary" type="button" disabled={saving} onClick={() => update({ logo: '', logo_mime: '' })}>移除 Logo</Button>
            <Button appearance="primary" type="submit" disabled={saving}>{saving ? '正在保存' : '保存品牌设置'}</Button>
          </div>
        </form>
      ) : null}
      {notice ? <MessageBar intent={notice.includes('已保存') ? 'success' : 'error'} role="alert"><MessageBarBody>{notice}</MessageBarBody></MessageBar> : null}
    </section>
  )
}
