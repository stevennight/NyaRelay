import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { ShieldCheck } from 'lucide-react'
import { useState } from 'react'
import { post } from '../api'

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
    <main className="auth-screen">
      <form
        className="auth-card"
        onSubmit={(event) => {
          event.preventDefault()
          setError('')
          setup.mutate()
        }}
      >
        <ShieldCheck size={32} />
        <h1>初始化控制器</h1>
        <label>
          <span>管理员账号</span>
          <input value={username} onChange={(event) => setUsername(event.target.value)} />
        </label>
        <label>
          <span>管理员密码</span>
          <input
            type="password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
        </label>
        {error && <p className="error">{error}</p>}
        <button type="submit" disabled={setup.isPending}>
          完成初始化
        </button>
      </form>
    </main>
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
    <main className="auth-screen">
      <form
        className="auth-card"
        onSubmit={(event) => {
          event.preventDefault()
          setError('')
          login.mutate()
        }}
      >
        <ShieldCheck size={32} />
        <h1>登录 NyaRelay</h1>
        <label>
          <span>账号</span>
          <input value={username} onChange={(event) => setUsername(event.target.value)} />
        </label>
        <label>
          <span>密码</span>
          <input
            type="password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
        </label>
        <label>
          <span>TOTP</span>
          <input
            value={totpCode}
            onChange={(event) => setTotpCode(event.target.value)}
            placeholder="启用后填写"
          />
        </label>
        {error && <p className="error">{error}</p>}
        <button type="submit" disabled={login.isPending}>
          登录
        </button>
      </form>
    </main>
  )
}
