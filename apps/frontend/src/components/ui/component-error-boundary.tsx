import { AlertTriangle, RotateCcw } from 'lucide-react'
import { Component, Fragment, type ErrorInfo, type ReactNode } from 'react'

import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { cn } from '@/lib/utils'

export type ComponentErrorBoundaryProps = {
  children: ReactNode
  className?: string
  label: string
  onError?: (error: Error, info: ErrorInfo) => void
  onRecover?: () => void
  resetKeys?: unknown[]
  scope?: 'page' | 'widget'
}

type ComponentErrorBoundaryState = {
  error: Error | null
  recoveryKey: number
}

function resetKeysChanged(previous: unknown[] = [], next: unknown[] = []) {
  if (previous.length !== next.length) {
    return true
  }

  for (let index = 0; index < previous.length; index += 1) {
    if (!Object.is(previous[index], next[index])) {
      return true
    }
  }

  return false
}

function ErrorFallback({
  className,
  label,
  onRecover,
  scope = 'widget',
}: Pick<ComponentErrorBoundaryProps, 'className' | 'label' | 'scope'> & {
  onRecover: () => void
}) {
  const isPage = scope === 'page'

  return (
    <Card
      className={cn(
        'grid justify-items-stretch items-center overflow-hidden bg-[linear-gradient(180deg,rgba(255,255,255,.06),rgba(255,255,255,.03))]',
        isPage ? 'min-h-[420px]' : 'min-h-[240px]',
        className,
      )}
      data-scope={scope}
    >
      <Empty
        className={cn(
          'border-none bg-transparent',
          isPage ? 'p-[var(--panel-padding)]' : 'min-h-[240px] p-6',
        )}
      >
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <AlertTriangle className="size-6" />
          </EmptyMedia>
          <EmptyTitle>Couldn&apos;t render {label}</EmptyTitle>
          <EmptyDescription>
            {isPage
              ? 'Reload this page section to retry the render while keeping the rest of the dashboard available.'
              : 'Reload this component to retry the render while keeping the rest of the page available.'}
          </EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button onClick={onRecover} type="button" variant="secondary">
            <RotateCcw className="size-4" />
            Reload {label}
          </Button>
        </EmptyContent>
      </Empty>
    </Card>
  )
}

class ComponentErrorBoundaryInner extends Component<ComponentErrorBoundaryProps, ComponentErrorBoundaryState> {
  state: ComponentErrorBoundaryState = {
    error: null,
    recoveryKey: 0,
  }

  static getDerivedStateFromError(error: Error): Pick<ComponentErrorBoundaryState, 'error'> {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    this.props.onError?.(error, info)
  }

  componentDidUpdate(previousProps: ComponentErrorBoundaryProps) {
    if (!this.state.error) {
      return
    }

    if (resetKeysChanged(previousProps.resetKeys, this.props.resetKeys)) {
      this.setState((current) => ({
        error: null,
        recoveryKey: current.recoveryKey + 1,
      }))
    }
  }

  private handleRecover = () => {
    this.props.onRecover?.()
    this.setState((current) => ({
      error: null,
      recoveryKey: current.recoveryKey + 1,
    }))
  }

  render() {
    if (this.state.error) {
      return (
        <ErrorFallback
          className={this.props.className}
          label={this.props.label}
          onRecover={this.handleRecover}
          scope={this.props.scope}
        />
      )
    }

    return <Fragment key={this.state.recoveryKey}>{this.props.children}</Fragment>
  }
}

export function ComponentErrorBoundary(props: ComponentErrorBoundaryProps) {
  return <ComponentErrorBoundaryInner {...props} />
}
