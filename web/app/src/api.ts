export class ApiError extends Error {
  status: number

  constructor(message: string, status: number) {
    super(message)
    this.status = status
  }
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(init.headers ?? {}),
    },
    credentials: 'same-origin',
  })
  if (!response.ok) {
    const body = await response.json().catch(() => ({}))
    throw new ApiError(translateApiError(body.error ?? response.statusText), response.status)
  }
  return response.json() as Promise<T>
}

export function post<T>(path: string, body: unknown): Promise<T> {
  return api<T>(path, { method: 'POST', body: JSON.stringify(body) })
}

const ERROR_TRANSLATIONS: Array<[RegExp, (m: RegExpMatchArray) => string]> = [
  [/^invalid username or password$/, () => '用户名或密码错误'],
  [/^node not found$/, () => '节点不存在'],
  [/^tunnel not found$/, () => '隧道不存在'],
  [/^tunnel is disabled$/, () => '隧道已停用'],
  [/^tunnel has too many stages \(maximum (\d+)\)$/, (m) => `隧道路由层数超过上限（最多 ${m[1]} 层）`],
  [/^stage (\d+) has too many nodes \(maximum (\d+)\)$/, (m) => `第 ${Number(m[1]) + 1} 层候选节点数超过上限（最多 ${m[2]} 个）`],
  [/^stage (\d+) must have at least one node$/, (m) => `第 ${Number(m[1]) + 1} 层至少需要一个候选节点`],
  [/^stage (\d+) node id is required$/, (m) => `第 ${Number(m[1]) + 1} 层需要选择节点`],
  [/^node (\S+) appears more than once in tunnel$/, (m) => `节点 ${m[1]} 在隧道中重复出现`],
  [/^node (\S+) not found$/, (m) => `节点 ${m[1]} 不存在`],
  [/^node (\S+) requires public_host, reported system\.ip, or connect_addr for chain stage$/, (m) => `节点 ${m[1]} 需要配置公网地址（public_host）、上报的系统 IP 或连接地址才能用于链式隧道`],
  [/^unsupported forward protocol "([^"]+)"$/, (m) => `不支持的转发协议：${m[1]}`],
  [/^forward protocol is required$/, () => '需要选择转发协议'],
  [/^target and targets cannot both be set$/, () => '不能同时设置单目标和目标列表'],
  [/^forward target is required$/, () => '需要至少一个转发目标'],
  [/^forward has too many targets \(maximum (\d+)\)$/, (m) => `转发目标数超过上限（最多 ${m[1]} 个）`],
  [/^duplicate forward target id "([^"]+)"$/, (m) => `目标 ID 重复：${m[1]}`],
  [/^unsupported protocol "([^"]+)"$/, (m) => `不支持的协议：${m[1]}`],
  [/^stage (\d+) has no (\w+) candidate$/, (m) => `第 ${Number(m[1]) + 1} 层没有可用的 ${m[2].toUpperCase()} 候选节点`],
  [/^tunnel has no entry node$/, () => '隧道没有入口节点'],
  [/^entry node not found$/, () => '入口节点不存在'],
  [/^tunnel has no entry node for forward protocols$/, () => '隧道没有支持该协议的入口节点'],
  [/^no entry nodes available$/, () => '没有可用的入口节点'],
  [/^no common forward port available across entry nodes$/, () => '入口节点之间没有共同可用的端口'],
  [/^listen port (\d+)\/(\w+) is already in use on node (\S+)$/, (m) => `端口 ${m[1]}/${m[2].toUpperCase()} 已被节点 ${m[3]} 占用`],
  [/^no free (\w+) port available on node (\S+)$/, (m) => `节点 ${m[2]} 没有空闲的 ${m[1].toUpperCase()} 端口`],
  [/^port (\d+) is outside node (\S+) range (\d+)-(\d+)$/, (m) => `端口 ${m[1]} 超出节点 ${m[2]} 的可用端口范围（${m[3]}-${m[4]}）`],
  [/^invalid address "([^"]+)":/, (m) => `地址格式不正确：${m[1]}`],
  [/^invalid port "([^"]+)"$/, (m) => `端口格式不正确：${m[1]}`],
  [/^username is required$/, () => '需要填写用户名'],
  [/^username is invalid$/, () => '用户名格式不正确'],
  [/^node has too many labels \(maximum (\d+)\)$/, (m) => `标签数量超过上限（最多 ${m[1]} 个）`],
  [/^node public host must be an IP address or hostname$/, () => '节点公网地址必须是 IP 或域名'],
  [/^node id does not match authenticated node$/, () => '节点 ID 与认证信息不匹配'],
  [/^node IP is invalid$/, () => '节点 IP 格式不正确'],
]

export function translateApiError(message: string): string {
  for (const [pattern, translate] of ERROR_TRANSLATIONS) {
    const match = message.match(pattern)
    if (match) return translate(match)
  }
  return message
}
