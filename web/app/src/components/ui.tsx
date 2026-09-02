import { AlertTriangle, ArrowDown, ArrowUp, ArrowUpDown, Inbox, LoaderCircle, X } from 'lucide-react'
import type { Dispatch, ReactNode, SetStateAction } from 'react'
import {
  cloneElement,
  createContext,
  Fragment,
  isValidElement,
  useCallback,
  useContext,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from 'react'

export function PageFrame({
  title,
  subtitle,
  action,
  children,
}: {
  title: string
  subtitle?: string
  action?: ReactNode
  children: ReactNode
}) {
  const titleID = useId()
  return (
    <main className="page-frame" aria-labelledby={titleID}>
      <header className="page-header">
        <div className="page-title">
          <h1 id={titleID}>{title}</h1>
          {subtitle && <p>{subtitle}</p>}
        </div>
        {action && <div className="page-action">{action}</div>}
      </header>
      {children}
    </main>
  )
}

export function Panel({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <section className={`panel ${className}`.trim()}>{children}</section>
}

export type ConfirmOptions = {
  title: string
  description: string
  confirmLabel?: string
  tone?: 'default' | 'danger'
}

type ConfirmRequest = (options: ConfirmOptions) => Promise<boolean>

const ConfirmContext = createContext<ConfirmRequest | null>(null)

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [request, setRequest] = useState<ConfirmOptions | null>(null)
  const resolveRef = useRef<((confirmed: boolean) => void) | null>(null)
  const confirm = useCallback<ConfirmRequest>((options) => new Promise((resolve) => {
    resolveRef.current?.(false)
    resolveRef.current = resolve
    setRequest(options)
  }), [])
  const finish = useCallback((confirmed: boolean) => {
    const resolve = resolveRef.current
    resolveRef.current = null
    setRequest(null)
    resolve?.(confirmed)
  }, [])

  useEffect(() => () => resolveRef.current?.(false), [])

  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      {request && (
        <Modal title={request.title} onClose={() => finish(false)}>
          <div className="confirm-copy">
            <span className={`confirm-icon${request.tone === 'danger' ? ' danger' : ''}`} aria-hidden="true">
              <AlertTriangle size={21} />
            </span>
            <p>{request.description}</p>
          </div>
          <div className="confirm-actions">
            <button className="ghost" type="button" onClick={() => finish(false)}>取消</button>
            <button
              className={request.tone === 'danger' ? 'confirm-danger' : ''}
              type="button"
              onClick={() => finish(true)}
            >
              {request.confirmLabel ?? '确认'}
            </button>
          </div>
        </Modal>
      )}
    </ConfirmContext.Provider>
  )
}

export function useConfirm(): ConfirmRequest {
  const confirm = useContext(ConfirmContext)
  return useCallback((options: ConfirmOptions) => {
    if (confirm) return confirm(options)
    return Promise.resolve(window.confirm(options.description))
  }, [confirm])
}

export function Banner({ text }: { text: string }) {
  return (
    <div className="banner" role="alert">
      <AlertTriangle size={18} aria-hidden="true" />
      <span>{text}</span>
    </div>
  )
}

export function Table({
  headers,
  children,
}: {
  headers: string[]
  children: ReactNode
}) {
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            {headers.map((header) => (
              <th key={header}>{header}</th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  )
}

export type SortValue = string | number | null | undefined

export type SortableTableColumn<T> = {
  key: string
  label: string
  getValue: (item: T) => SortValue
  sortable?: boolean
}

export function SortableTable<T>({
  items,
  columns,
  getRowKey,
  defaultSortKey,
  storageKey,
  children,
}: {
  items: T[]
  columns: SortableTableColumn<T>[]
  getRowKey: (item: T) => string
  defaultSortKey?: string
  storageKey?: string
  children: (item: T) => ReactNode
}) {
  const initialSortKey = defaultSortKey ?? columns.find((column) => column.sortable !== false)?.key ?? ''
  const [sort, setSort] = useState<{ key: string; direction: 'asc' | 'desc' }>(() => {
    if (storageKey) {
      try {
        const stored = sessionStorage.getItem(`nyarelay:table-sort:${storageKey}`)
        if (stored) {
          const parsed = JSON.parse(stored) as { key?: unknown; direction?: unknown }
          const storedColumn = typeof parsed.key === 'string'
            ? columns.find((column) => column.key === parsed.key && column.sortable !== false)
            : undefined
          if (storedColumn && (parsed.direction === 'asc' || parsed.direction === 'desc')) {
            return { key: storedColumn.key, direction: parsed.direction }
          }
        }
      } catch {
        // Storage can be unavailable in privacy-restricted browser contexts.
      }
    }
    return { key: initialSortKey, direction: 'asc' }
  })
  useEffect(() => {
    const hasValidSort = columns.some((column) => column.key === sort.key && column.sortable !== false)
    if (hasValidSort || (sort.key === initialSortKey && sort.direction === 'asc')) return
    setSort({ key: initialSortKey, direction: 'asc' })
  }, [columns, initialSortKey, sort.direction, sort.key])
  useEffect(() => {
    if (!storageKey) return
    try {
      sessionStorage.setItem(`nyarelay:table-sort:${storageKey}`, JSON.stringify(sort))
    } catch {
      // Storage can be unavailable in privacy-restricted browser contexts.
    }
  }, [sort, storageKey])
  const sortedItems = useMemo(() => {
    const column = columns.find((candidate) => candidate.key === sort.key && candidate.sortable !== false)
    if (!column) return items

    const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })
    return items
      .map((item, index) => ({ item, index }))
      .sort((left, right) => {
        const leftValue = column.getValue(left.item)
        const rightValue = column.getValue(right.item)
        let comparison = 0

        if (leftValue == null && rightValue != null) return 1
        if (leftValue != null && rightValue == null) return -1
        if (typeof leftValue === 'number' && typeof rightValue === 'number') comparison = leftValue - rightValue
        else comparison = collator.compare(String(leftValue ?? ''), String(rightValue ?? ''))

        return (comparison || left.index - right.index) * (sort.direction === 'asc' ? 1 : -1)
      })
      .map(({ item }) => item)
  }, [columns, items, sort])

  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            {columns.map((column) => {
              const active = column.key === sort.key && column.sortable !== false
              const direction = active ? sort.direction : undefined
              return (
                <th
                  key={column.key}
                  aria-sort={direction === 'asc' ? 'ascending' : direction === 'desc' ? 'descending' : 'none'}
                >
                  {column.sortable === false ? (
                    column.label
                  ) : (
                    <button
                      className={`table-sort${active ? ' active' : ''}`}
                      type="button"
                      onClick={() => setSort((current) => ({
                        key: column.key,
                        direction: current.key === column.key && current.direction === 'asc' ? 'desc' : 'asc',
                      }))}
                      aria-label={`${column.label} ${direction === 'asc' ? 'descending' : 'ascending'}`}
                    >
                      <span>{column.label}</span>
                      {direction === 'asc' ? <ArrowUp size={14} /> : direction === 'desc' ? <ArrowDown size={14} /> : <ArrowUpDown size={14} />}
                    </button>
                  )}
                </th>
              )
            })}
          </tr>
        </thead>
        <tbody>
          {sortedItems.map((item) => (
            <Fragment key={getRowKey(item)}>{children(item)}</Fragment>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function Stat({
  label,
  value,
  icon,
  tone = 'default',
}: {
  label: string
  value: string
  icon?: ReactNode
  tone?: 'default' | 'success' | 'teal' | 'amber' | 'violet'
}) {
  return (
    <div className={`stat stat-${tone}`}>
      <div className="stat-label">
        <span>{label}</span>
        {icon && <span className="stat-icon">{icon}</span>}
      </div>
      <strong>{value}</strong>
    </div>
  )
}

export function StatusPill({ value }: { value: string }) {
  const labels: Record<string, string> = {
    online: '在线',
    offline: '离线',
    revoked: '已吊销',
    enabled: '已启用',
    disabled: '已停用',
  }
  return (
    <span className={`status ${value}`.trim()}>
      <span className="status-dot" aria-hidden="true" />
      {labels[value] ?? value}
    </span>
  )
}

export function FieldGrid({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <div className={`field-grid ${className}`.trim()}>{children}</div>
}

export function Field({
  label,
  children,
  wide,
  hint,
}: {
  label: string
  children: ReactNode
  wide?: boolean
  hint?: string
}) {
  const controlID = useId()
  const hintID = useId()
  const control = isValidElement<{ id?: string; 'aria-describedby'?: string }>(children)
    ? cloneElement(children, {
      id: children.props.id ?? controlID,
      'aria-describedby': hint
        ? [children.props['aria-describedby'], hintID].filter(Boolean).join(' ')
        : children.props['aria-describedby'],
    })
    : children

  return (
    <div className={`field ${wide ? 'wide-field' : ''}`.trim()}>
      <label htmlFor={isValidElement<{ id?: string }>(children) ? children.props.id ?? controlID : undefined}>
        {label}
      </label>
      {control}
      {hint && <small id={hintID}>{hint}</small>}
    </div>
  )
}

export function ToggleField({
  label,
  description,
  checked,
  onChange,
  disabled,
  ariaLabel,
}: {
  label: string
  description?: string
  checked: boolean
  onChange: (checked: boolean) => void
  disabled?: boolean
  ariaLabel?: string
}) {
  return (
    <label className="toggle-field">
      <input
        type="checkbox"
        aria-label={ariaLabel ?? label}
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
      />
      <span className="toggle-control" aria-hidden="true" />
      <span className="toggle-copy">
        <strong>{label}</strong>
        {description && <small>{description}</small>}
      </span>
    </label>
  )
}

export function InlineActions({ children }: { children: ReactNode }) {
  return <div className="actions">{children}</div>
}

export function FormActions({ children }: { children: ReactNode }) {
  return <div className="form-actions">{children}</div>
}

export function Modal({
  title,
  subtitle,
  action,
  children,
  onClose,
  size = 'md',
}: {
  title: string
  subtitle?: string
  action?: ReactNode
  children: ReactNode
  onClose: () => void
  size?: 'md' | 'lg'
}) {
  const titleID = useId()
  const subtitleID = useId()
  const dialogRef = useRef<HTMLElement>(null)
  const closeButtonRef = useRef<HTMLButtonElement>(null)
  const onCloseRef = useRef(onClose)

  useEffect(() => {
    onCloseRef.current = onClose
  }, [onClose])

  useEffect(() => {
    const previousOverflow = document.body.style.overflow
    const previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null
    document.body.style.overflow = 'hidden'
    closeButtonRef.current?.focus()
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        onCloseRef.current()
        return
      }
      if (event.key !== 'Tab') return

      const focusable = Array.from(dialogRef.current?.querySelectorAll<HTMLElement>([
        'button:not([disabled])',
        '[href]',
        'input:not([disabled])',
        'select:not([disabled])',
        'textarea:not([disabled])',
        '[tabindex]:not([tabindex="-1"])',
      ].join(',')) ?? []).filter((element) => element.tabIndex >= 0)
      if (focusable.length === 0) {
        event.preventDefault()
        closeButtonRef.current?.focus()
        return
      }

      const activeIndex = focusable.indexOf(document.activeElement as HTMLElement)
      if (event.shiftKey) {
        if (activeIndex <= 0) {
          event.preventDefault()
          focusable[focusable.length - 1].focus()
        }
      } else if (activeIndex < 0 || activeIndex === focusable.length - 1) {
        event.preventDefault()
        focusable[0].focus()
      }
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.body.style.overflow = previousOverflow
      document.removeEventListener('keydown', handleKeyDown)
      if (previouslyFocused?.isConnected) previouslyFocused.focus()
    }
  }, [])

  return (
    <div
      className="modal-backdrop"
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) {
          onCloseRef.current()
        }
      }}
    >
      <section
        ref={dialogRef}
        className={`modal modal-${size}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleID}
        aria-describedby={subtitle ? subtitleID : undefined}
      >
        <header className="modal-header">
          <div className="modal-title">
            <h2 id={titleID}>{title}</h2>
            {subtitle && <p id={subtitleID}>{subtitle}</p>}
          </div>
          <div className="modal-header-actions">
            {action && <div className="modal-header-action-slot">{action}</div>}
            <button ref={closeButtonRef} className="icon-button" type="button" aria-label="关闭" onClick={() => onCloseRef.current()}>
              <X size={18} />
            </button>
          </div>
        </header>
        <div className="modal-body">{children}</div>
      </section>
    </div>
  )
}

export function DetailGrid({ items }: { items: Array<{ label: string; value: ReactNode }> }) {
  return (
    <dl className="detail-grid">
      {items.map((item) => (
        <div key={item.label}>
          <dt>{item.label}</dt>
          <dd>{item.value}</dd>
        </div>
      ))}
    </dl>
  )
}

export function EmptyState({
  title,
  text,
  action,
  icon,
}: {
  title: string
  text: string
  action?: ReactNode
  icon?: ReactNode
}) {
  return (
    <section className="panel empty-state">
      <div className="empty-state-icon" aria-hidden="true">{icon ?? <Inbox size={22} />}</div>
      <div className="empty-state-copy">
        <h2>{title}</h2>
        <p>{text}</p>
      </div>
      {action && <div className="empty-state-action">{action}</div>}
    </section>
  )
}

export function LoadingState({
  label = '正在加载',
  rows = 4,
}: {
  label?: string
  rows?: number
}) {
  return (
    <section className="loading-state panel" aria-busy="true" aria-live="polite">
      <div className="loading-state-heading">
        <LoaderCircle className="spin" size={18} aria-hidden="true" />
        <span>{label}</span>
      </div>
      <div className="skeleton-list" aria-hidden="true">
        {Array.from({ length: rows }, (_, index) => (
          <div className="skeleton-row" key={index}>
            <span />
            <span />
            <span />
          </div>
        ))}
      </div>
    </section>
  )
}

export function useSessionState<T>(
  key: string,
  initialValue: T | (() => T),
  validate?: (value: unknown) => value is T,
): [T, Dispatch<SetStateAction<T>>] {
  const [value, setValue] = useState<T>(() => {
    try {
      const stored = sessionStorage.getItem(`nyarelay:state:${key}`)
      if (stored) {
        const parsed = JSON.parse(stored) as unknown
        if (!validate || validate(parsed)) return parsed as T
      }
    } catch {
      // Storage can be unavailable in privacy-restricted browser contexts.
    }
    return typeof initialValue === 'function'
      ? (initialValue as () => T)()
      : initialValue
  })

  useEffect(() => {
    try {
      sessionStorage.setItem(`nyarelay:state:${key}`, JSON.stringify(value))
    } catch {
      // Storage can be unavailable in privacy-restricted browser contexts.
    }
  }, [key, value])

  return [value, setValue]
}

export function Subnav({
  children,
}: {
  children: ReactNode
}) {
  return <div className="subnav">{children}</div>
}

export function formatTime(value?: string) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime()) || date.getUTCFullYear() <= 1) return '-'
  return date.toLocaleString()
}

export function formatBytes(value: number) {
  if (!value) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = value
  let index = 0
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024
    index += 1
  }
  return `${size.toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}
