import { type ComponentPropsWithoutRef } from 'react'
import { type Components } from 'react-markdown'
import ReactMarkdown from 'react-markdown'
import remarkBreaks from 'remark-breaks'
import remarkGfm from 'remark-gfm'

import { cn } from '@/lib/utils'

function isExternalHref(href?: string) {
  return typeof href === 'string' && /^(?:[a-z][a-z0-9+.-]*:|\/\/)/i.test(href)
}

export const wrappedOutputClassName = 'whitespace-pre-wrap break-words [overflow-wrap:anywhere]'

const markdownComponents: Components = {
  a({ children, href, ...props }) {
    const { node, ...anchorProps } = props as ComponentPropsWithoutRef<'a'> & { node?: unknown }
    void node
    const external = isExternalHref(href)

    return (
      <a
        className="font-medium text-inherit underline decoration-current/50 underline-offset-2 transition hover:decoration-current"
        href={href}
        rel={external ? 'noreferrer noopener' : anchorProps.rel}
        target={external ? '_blank' : anchorProps.target}
        {...anchorProps}
      >
        {children}
      </a>
    )
  },
  blockquote({ children }) {
    return <blockquote className="border-l border-current/20 pl-3 opacity-90">{children}</blockquote>
  },
  code({ children, className, ...props }) {
    const { inline, node, ...codeProps } = props as ComponentPropsWithoutRef<'code'> & { inline?: boolean; node?: unknown }
    void node

    if (inline) {
      return (
        <code className={cn('rounded bg-white/10 px-1 py-0.5 font-mono text-[0.92em] text-inherit', className)} {...codeProps}>
          {children}
        </code>
      )
    }

    return (
      <code className={cn('font-mono text-inherit', className)} {...codeProps}>
        {children}
      </code>
    )
  },
  del({ children }) {
    return <del className="opacity-70">{children}</del>
  },
  em({ children }) {
    return <em className="italic text-inherit">{children}</em>
  },
  h1({ children }) {
    return <h1 className="mt-3 text-[1.2em] font-semibold leading-6 text-inherit first:mt-0">{children}</h1>
  },
  h2({ children }) {
    return <h2 className="mt-3 text-[1.15em] font-semibold leading-6 text-inherit first:mt-0">{children}</h2>
  },
  h3({ children }) {
    return <h3 className="mt-3 text-[1.08em] font-semibold leading-6 text-inherit first:mt-0">{children}</h3>
  },
  h4({ children }) {
    return <h4 className="mt-3 text-[1.04em] font-semibold leading-6 text-inherit first:mt-0">{children}</h4>
  },
  h5({ children }) {
    return <h5 className="mt-3 text-[1.02em] font-semibold leading-6 text-inherit first:mt-0">{children}</h5>
  },
  h6({ children }) {
    return <h6 className="mt-3 text-[1em] font-semibold leading-6 text-inherit first:mt-0">{children}</h6>
  },
  hr() {
    return <hr className="border-current/15" />
  },
  input({ className, ...props }) {
    const { node, checked, type, ...inputProps } = props as ComponentPropsWithoutRef<'input'> & { node?: unknown }
    void node

    if (type !== 'checkbox') {
      return null
    }

    return (
      <input
        checked={Boolean(checked)}
        className={cn('pointer-events-none mt-0.5 size-4 shrink-0 accent-[var(--accent)]', className)}
        disabled
        readOnly
        type="checkbox"
        {...inputProps}
      />
    )
  },
  li({ className, children }) {
    const taskListItem = typeof className === 'string' && className.includes('task-list-item')

    return <li className={cn(taskListItem ? 'flex items-start gap-2 leading-6' : 'leading-6', className)}>{children}</li>
  },
  ol({ children }) {
    return <ol className="m-0 list-decimal space-y-1 pl-5">{children}</ol>
  },
  p({ children }) {
    return <p className="m-0 leading-6 text-inherit">{children}</p>
  },
  pre({ children }) {
    return <pre className={cn(wrappedOutputClassName, 'rounded-md border border-white/10 bg-black/35 p-3 text-inherit')}>{children}</pre>
  },
  table({ children }) {
    return (
      <div className="overflow-x-auto">
        <table className="w-full border-collapse text-left text-inherit">{children}</table>
      </div>
    )
  },
  tbody({ children }) {
    return <tbody className="divide-y divide-white/10">{children}</tbody>
  },
  td({ children }) {
    return <td className="border border-white/10 px-3 py-2 align-top">{children}</td>
  },
  th({ children }) {
    return <th className="border border-white/10 px-3 py-2 font-semibold">{children}</th>
  },
  thead({ children }) {
    return <thead className="bg-white/5">{children}</thead>
  },
  tr({ children }) {
    return <tr>{children}</tr>
  },
  ul({ className, children }) {
    const taskList = typeof className === 'string' && className.includes('contains-task-list')

    return <ul className={cn('m-0 space-y-1', taskList ? 'list-none pl-0' : 'list-disc pl-5', className)}>{children}</ul>
  },
  strong({ children }) {
    return <strong className="font-semibold text-inherit">{children}</strong>
  },
}

export function MarkdownText({ content, className }: { content: string; className?: string }) {
  if (!content.trim()) {
    return null
  }

  return (
    <div className={cn('min-w-0 space-y-3 break-words [overflow-wrap:anywhere] whitespace-normal', className)}>
      <ReactMarkdown
        allowedElements={[
          'a',
          'blockquote',
          'br',
          'code',
          'del',
          'em',
          'h1',
          'h2',
          'h3',
          'h4',
          'h5',
          'h6',
          'hr',
          'input',
          'li',
          'ol',
          'p',
          'pre',
          'strong',
          'table',
          'tbody',
          'td',
          'th',
          'thead',
          'tr',
          'ul',
        ]}
        components={markdownComponents}
        remarkPlugins={[remarkGfm, remarkBreaks]}
        unwrapDisallowed
      >
        {content}
      </ReactMarkdown>
    </div>
  )
}

export { MarkdownText as Markdown }
