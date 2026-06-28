import type { ReactNode } from 'react'
import { cloneElement, isValidElement, useId } from 'react'

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
  return (
    <main className="page-frame">
      <header className="page-header">
        <div className="page-title">
          <h1>{title}</h1>
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

export function Banner({ text }: { text: string }) {
  return <div className="banner">{text}</div>
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

export function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="stat">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  )
}

export function StatusPill({ value }: { value: string }) {
  return <span className={`status ${value}`.trim()}>{value}</span>
}

export function FieldGrid({ children }: { children: ReactNode }) {
  return <div className="field-grid">{children}</div>
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
}: {
  label: string
  description?: string
  checked: boolean
  onChange: (checked: boolean) => void
  disabled?: boolean
}) {
  return (
    <label className="toggle-field">
      <input
        type="checkbox"
        aria-label={label}
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
}: {
  title: string
  text: string
  action?: ReactNode
}) {
  return (
    <section className="panel empty-state">
      <h2>{title}</h2>
      <p>{text}</p>
      {action}
    </section>
  )
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
  if (Number.isNaN(date.getTime())) return '-'
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
