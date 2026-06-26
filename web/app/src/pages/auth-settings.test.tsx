import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { LoginPage, SetupPage } from './auth'
import { ControllerSettingsPage, SecuritySettingsPage } from './settings'
import { installFetch, jsonResponse, renderWithClient } from '../test/helpers'
import { routerMocks } from '../test/router-mocks'

describe('auth and security flows', () => {
  it('initializes the controller and navigates to the dashboard', async () => {
    installFetch([
      {
        path: '/api/setup',
        method: 'POST',
        response: jsonResponse({ user: { id: 1, username: 'admin' } }),
      },
    ])

    renderWithClient(<SetupPage />)

    fireEvent.change(screen.getByLabelText('管理员账号'), { target: { value: 'root' } })
    fireEvent.change(screen.getByLabelText('管理员密码'), { target: { value: 'secret' } })
    fireEvent.click(screen.getByRole('button', { name: '完成初始化' }))

    await waitFor(() => {
      expect(routerMocks.navigate).toHaveBeenCalledWith({ to: '/dashboard', replace: true })
    })
  })

  it('logs in with TOTP and navigates to the dashboard', async () => {
    const fetchMock = installFetch([
      {
        path: '/api/auth/login',
        method: 'POST',
        response: jsonResponse({ user: { id: 1, username: 'admin' } }),
      },
    ])

    renderWithClient(<LoginPage />)

    fireEvent.change(screen.getByLabelText('账号'), { target: { value: 'admin' } })
    fireEvent.change(screen.getByLabelText('密码'), { target: { value: 'hunter2' } })
    fireEvent.change(screen.getByLabelText('TOTP'), { target: { value: '123456' } })
    fireEvent.click(screen.getByRole('button', { name: '登录' }))

    await waitFor(() => {
      expect(routerMocks.navigate).toHaveBeenCalledWith({ to: '/dashboard', replace: true })
    })

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [, init] = fetchMock.mock.calls[0]
    expect(JSON.parse(String(init?.body))).toEqual({
      username: 'admin',
      password: 'hunter2',
      totp_code: '123456',
    })
  })

  it('generates and enables TOTP from settings', async () => {
    const fetchMock = installFetch([
      {
        path: '/api/settings/totp/setup',
        method: 'POST',
        response: jsonResponse({
          secret: 'BASE32SECRET',
          url: 'otpauth://totp/NyaRelay:admin?secret=BASE32SECRET',
        }),
      },
      {
        path: '/api/settings/totp/enable',
        method: 'POST',
        response: jsonResponse({ ok: true }),
      },
      {
        path: '/api/settings/totp/disable',
        method: 'POST',
        response: jsonResponse({ ok: true }),
      },
      {
        path: '/api/controller/info',
        method: 'GET',
        response: jsonResponse({
          signing_key: 'pub-key',
          public_url: 'https://relay.example.com',
          revision: 7,
        }),
      },
    ])

    renderWithClient(<SecuritySettingsPage />)

    fireEvent.click(screen.getByRole('button', { name: '生成 TOTP 密钥' }))
    expect(await screen.findByText(/secret: BASE32SECRET/)).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('验证码'), { target: { value: '654321' } })
    fireEvent.click(screen.getByRole('button', { name: '启用' }))

    expect(await screen.findByText('TOTP 已启用')).toBeInTheDocument()
    expect(fetchMock.mock.calls.some(([path]) => path === '/api/settings/totp/enable')).toBe(true)
    const enableCall = fetchMock.mock.calls.find(([path]) => path === '/api/settings/totp/enable')
    expect(enableCall).toBeDefined()
    expect(JSON.parse(String(enableCall?.[1]?.body))).toEqual({ code: '654321' })
  })

  it('updates the controller public URL from settings', async () => {
    const fetchMock = installFetch([
      {
        path: '/api/controller/info',
        method: 'GET',
        response: jsonResponse({
          signing_key: 'pub-key',
          public_url: 'https://old.example.com',
          revision: 7,
        }),
      },
      {
        path: '/api/controller/info',
        method: 'POST',
        response: jsonResponse({ public_url: 'https://new.example.com' }),
      },
    ])

    renderWithClient(<ControllerSettingsPage />)

    expect(await screen.findByDisplayValue('https://old.example.com')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('面板公开地址'), { target: { value: 'https://new.example.com' } })
    fireEvent.click(screen.getByRole('button', { name: '保存公开地址' }))

    expect(await screen.findByText('已保存')).toBeInTheDocument()
    const saveCall = fetchMock.mock.calls.find(([path, init]) => path === '/api/controller/info' && init?.method === 'POST')
    expect(JSON.parse(String(saveCall?.[1]?.body))).toEqual({ public_url: 'https://new.example.com' })
  })
})
