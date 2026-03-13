import { useEffect, useMemo, useRef } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { FolderKanban, LayoutDashboard, ListTodo, MonitorPlay, RefreshCw } from 'lucide-react'

import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
  CommandShortcut,
} from '@/components/ui/command'
import { DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { api } from '@/lib/api'
import { appRoutes } from '@/lib/routes'

const navigationItems = [
  { label: 'Overview', to: appRoutes.overview, icon: LayoutDashboard },
  { label: 'Work', to: appRoutes.work, icon: ListTodo },
  { label: 'Projects', to: appRoutes.projects, icon: FolderKanban },
  { label: 'Sessions', to: appRoutes.sessions, icon: MonitorPlay },
]

export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const bootstrap = useQuery({ queryKey: ['bootstrap'], queryFn: api.bootstrap })
  const inputRef = useRef<HTMLInputElement>(null)
  const recentIssues = useMemo(() => bootstrap.data?.issues.items.slice(0, 8) ?? [], [bootstrap.data?.issues.items])

  useEffect(() => {
    if (!open) {
      return
    }

    const rafId = window.requestAnimationFrame(() => {
      inputRef.current?.focus()
    })

    return () => window.cancelAnimationFrame(rafId)
  }, [open])

  return (
    <CommandDialog open={open} onOpenChange={onOpenChange}>
      <DialogTitle className="sr-only">Command palette</DialogTitle>
      <DialogDescription className="sr-only">Search navigation, issues, and quick actions.</DialogDescription>
      <Command>
        <CommandInput ref={inputRef} placeholder="Search issues, projects, sessions, or actions..." />
        <CommandList>
          <CommandEmpty>No results found.</CommandEmpty>

          <CommandGroup heading="Navigate">
            {navigationItems.map((item) => {
              const Icon = item.icon
              return (
                <CommandItem
                  key={item.to}
                  onSelect={() => {
                    onOpenChange(false)
                    void navigate({ to: item.to })
                  }}
                >
                  <Icon className="size-4 text-[var(--accent)]" />
                  <span>{item.label}</span>
                </CommandItem>
              )
            })}
          </CommandGroup>

          <CommandSeparator />

          <CommandGroup heading="Actions">
            <CommandItem
              onSelect={() => {
                onOpenChange(false)
                void queryClient.invalidateQueries()
              }}
            >
              <RefreshCw className="size-4 text-[var(--accent)]" />
              <span>Refresh data</span>
              <CommandShortcut>R</CommandShortcut>
            </CommandItem>
          </CommandGroup>

          {recentIssues.length > 0 ? (
            <>
              <CommandSeparator />
              <CommandGroup heading="Issues">
                {recentIssues.map((issue) => (
                  <CommandItem
                    key={issue.id}
                    onSelect={() => {
                      onOpenChange(false)
                      void navigate({ to: appRoutes.issueDetail, params: { identifier: issue.identifier } })
                    }}
                  >
                    <span className="font-mono text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{issue.identifier}</span>
                    <span className="truncate">{issue.title}</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            </>
          ) : null}
        </CommandList>
      </Command>
    </CommandDialog>
  )
}
