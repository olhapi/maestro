import type { ComponentProps } from 'react'

import { IssuePreviewSheet } from '@/components/dashboard/issue-preview-sheet'
import { ComponentErrorBoundary } from '@/components/ui/component-error-boundary'

type IssuePreviewBoundaryProps = ComponentProps<typeof IssuePreviewSheet>

export function IssuePreviewBoundary(props: IssuePreviewBoundaryProps) {
  return (
    <ComponentErrorBoundary
      className="min-h-[240px]"
      label="issue preview"
      resetKeys={[props.issue?.identifier ?? '', props.open]}
      scope="widget"
    >
      <IssuePreviewSheet {...props} />
    </ComponentErrorBoundary>
  )
}
