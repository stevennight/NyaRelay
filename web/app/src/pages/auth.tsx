import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { LoaderCircle, Network, ShieldCheck } from 'lucide-react'
import type { ReactNode } from 'react'
import { useState } from 'react'
import { post } from '../api'
import { Field } from '../components/ui'

export function SetupPage() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')

  const setup = useMutation({
    mutationFn: () => post('/api/setup', { username, password }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['bootstrap'] })
      navigate({ to: '/dashboard', replace: true })
    },
    onError: (err) => {
      setError(err instanceof Error ? err.message : '初始化失败')
    },
  })

  return (
    <AuthLayout>
      <form
        className="auth-form"
        onSubmit={(event) => {
          event.preventDefault()
          setError('')
          setup.mutate()
        }}
      >
        <AuthHeading title="初始化控制器" subtitle="创建首个管理员账号" />
        <div className="auth-fields">
          <Field label="管理员账号">
            <input
              autoComplete="username"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
            />
          </Field>
          <Field label="管理员密码">
            <input
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </Field>
        </div>
        {error && <p className="error" role="alert">{error}</p>}
        <button className="auth-submit" type="submit" disabled={setup.isPending} aria-busy={setup.isPending}>
          {setup.isPending && <LoaderCircle className="spin" size={17} />}
          {setup.isPending ? '正在初始化' : '完成初始化'}
        </button>
      </form>
    </AuthLayout>
  )
}

export function LoginPage() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [totpCode, setTotpCode] = useState('')
  const [error, setError] = useState('')

  const login = useMutation({
    mutationFn: () => post('/api/auth/login', { username, password, totp_code: totpCode }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['bootstrap'] })
      navigate({ to: '/dashboard', replace: true })
    },
    onError: (err) => {
      setError(err instanceof Error ? err.message : '登录失败')
    },
  })

  return (
    <AuthLayout>
      <form
        className="auth-form"
        onSubmit={(event) => {
          event.preventDefault()
          setError('')
          login.mutate()
        }}
      >
        <AuthHeading title="欢迎回来" subtitle="登录 NyaRelay 控制台" />
        <div className="auth-fields">
          <Field label="账号">
            <input
              autoComplete="username"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
            />
          </Field>
          <Field label="密码">
            <input
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </Field>
          <Field label="TOTP" hint="未启用双因素认证时可留空">
            <input
              value={totpCode}
              autoComplete="one-time-code"
              inputMode="numeric"
              onChange={(event) => setTotpCode(event.target.value)}
              placeholder="6 位验证码"
            />
          </Field>
        </div>
        {error && <p className="error" role="alert">{error}</p>}
        <button className="auth-submit" type="submit" disabled={login.isPending} aria-busy={login.isPending}>
          {login.isPending && <LoaderCircle className="spin" size={17} />}
          {login.isPending ? '正在登录' : '登录'}
        </button>
      </form>
    </AuthLayout>
  )
}

function AuthLayout({ children }: { children: ReactNode }) {
  return (
    <main className="auth-screen">
      <section className="auth-shell">
        <div className="auth-brand-panel">
          <div className="auth-brand-lockup">
            <span className="auth-brand-mark"><Network size={22} /></span>
            <strong>NyaRelay</strong>
          </div>
          <div className="auth-brand-copy">
            <span>PRIVATE RELAY CONSOLE</span>
            <h1>私有中继控制台</h1>
            <p>Controller workspace</p>
          </div>
          <div className="auth-signal" aria-hidden="true">
            <span />
            <span />
            <span />
            <i />
          </div>
        </div>
        <div className="auth-form-panel">{children}</div>
      </section>
    </main>
  )
}

function AuthHeading({ title, subtitle }: { title: string; subtitle: string }) {
  return (
    <div className="auth-heading">
      <span className="auth-heading-icon"><ShieldCheck size={20} /></span>
      <div>
        <h2>{title}</h2>
        <p>{subtitle}</p>
      </div>
    </div>
  )
}
