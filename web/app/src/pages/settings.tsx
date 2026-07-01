import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, Navigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { api, post } from '../api'
import type { ControllerInfo } from '../types'
import { Banner, DetailGrid, Field, FieldGrid, FormActions, InlineActions, PageFrame, Panel, Subnav } from '../components/ui'

export function SettingsIndexPage() {
  return <Navigate to="/settings/security" replace />
}

export function SecuritySettingsPage() {
  const queryClient = useQueryClient()
  const [totp, setTotp] = useState<{ secret: string; url: string } | null>(null)
  const [code, setCode] = useState('')
  const [message, setMessage] = useState('')

  const setup = useMutation({
    mutationFn: () => post('/api/settings/totp/setup', {}),
    onSuccess: (value) => setTotp(value as { secret: string; url: string }),
  })
  const enable = useMutation({
    mutationFn: () => post('/api/settings/totp/enable', { code }),
    onSuccess: async () => {
      setMessage('TOTP 已启用')
      setTotp(null)
      setCode('')
      await queryClient.invalidateQueries({ queryKey: ['bootstrap'] })
    },
  })
  const disable = useMutation({
    mutationFn: () => post('/api/settings/totp/disable', {}),
    onSuccess: async () => {
      setMessage('TOTP 已关闭')
      await queryClient.invalidateQueries({ queryKey: ['bootstrap'] })
    },
  })

  return (
    <PageFrame title="设置" subtitle="安全相关选项放在这里。">
      <SettingsTabs />
      <Panel>
        <h2>双因素认证</h2>
        <p>用任意 TOTP 应用扫描密钥，输入 6 位验证码后启用。</p>
        <InlineActions>
          <button onClick={() => setup.mutate()} disabled={setup.isPending}>生成 TOTP 密钥</button>
          <button className="ghost danger" onClick={() => disable.mutate()} disabled={disable.isPending}>
            关闭 TOTP
          </button>
        </InlineActions>
        {totp && <pre>{`${totp.url}\n\nsecret: ${totp.secret}`}</pre>}
        {totp && (
          <form
            className="inline-form"
            onSubmit={(event) => {
              event.preventDefault()
              enable.mutate()
            }}
          >
            <Field label="验证码">
              <input value={code} onChange={(event) => setCode(event.target.value)} placeholder="6 位验证码" />
            </Field>
            <button type="submit" disabled={enable.isPending}>启用</button>
          </form>
        )}
        {message && <p>{message}</p>}
      </Panel>
    </PageFrame>
  )
}

export function ControllerSettingsPage() {
  const queryClient = useQueryClient()
  const [publicUrl, setPublicUrl] = useState('')
  const [message, setMessage] = useState('')
  const info = useQuery({
    queryKey: ['controller-info'],
    queryFn: () => api<ControllerInfo>('/api/controller/info'),
  })
  const update = useMutation({
    mutationFn: () => post('/api/controller/info', { public_url: publicUrl }),
    onSuccess: async () => {
      setMessage('已保存')
      await queryClient.invalidateQueries({ queryKey: ['controller-info'] })
    },
    onError: (err) => setMessage(err instanceof Error ? err.message : '保存失败'),
  })

  useEffect(() => {
    if (info.data) {
      setPublicUrl(info.data.public_url ?? '')
    }
  }, [info.data])

  return (
    <PageFrame title="控制器" subtitle="这里显示控制器公钥、公开地址和当前配置版本。">
      <SettingsTabs />
      <Panel>
        <h2>控制器信息</h2>
        {info.error && <Banner text={info.error instanceof Error ? info.error.message : '加载失败'} />}
        {info.data && (
          <DetailGrid
            items={[
              { label: '公开地址', value: info.data.public_url || '-' },
              { label: '配置版本', value: String(info.data.revision) },
              { label: '应用版本', value: formatBuild(info.data.build) },
              { label: '内置 Node 版本', value: info.data.node_release?.manifest.version || '-' },
              { label: 'Node 自动更新', value: info.data.node_release?.update_enabled ? '已启用' : (info.data.node_release?.disabled_reason || '未启用') },
              { label: '签名公钥', value: <code>{info.data.signing_key}</code> },
            ]}
          />
        )}
      </Panel>
      <Panel>
        <h2>公开地址</h2>
        <form
          className="form"
          onSubmit={(event) => {
            event.preventDefault()
            setMessage('')
            update.mutate()
          }}
        >
          <FieldGrid>
            <Field label="面板公开地址" wide>
              <input
                value={publicUrl}
                onChange={(event) => setPublicUrl(event.target.value)}
                placeholder="https://relay.example.com"
              />
            </Field>
          </FieldGrid>
          <FormActions>
            <button type="submit" disabled={update.isPending}>保存公开地址</button>
          </FormActions>
        </form>
        {message && <p>{message}</p>}
      </Panel>
      <Panel>
        <h2>部署提醒</h2>
        <p>控制器通过 Caddy 暴露 HTTPS；node 只需要出站连接，不需要 SSH 或 Docker socket。</p>
      </Panel>
    </PageFrame>
  )
}

function formatBuild(build?: { version: string; commit?: string; build_date?: string }) {
  if (!build) return '-'
  const suffix = [build.commit, build.build_date].filter(Boolean).join(' / ')
  return suffix ? `${build.version} (${suffix})` : build.version
}

function SettingsTabs() {
  return (
    <Subnav>
      <Link to="/settings/security" className="subnav-link" activeProps={{ className: 'subnav-link active' }}>
        安全
      </Link>
      <Link to="/settings/controller" className="subnav-link" activeProps={{ className: 'subnav-link active' }}>
        控制器
      </Link>
    </Subnav>
  )
}
