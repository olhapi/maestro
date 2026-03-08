import { Command } from 'cmdk'
import { FolderKanban, LayoutDashboard, ListTodo, MonitorPlay, Search } from 'lucide-react'
import { useNavigate } from '@tanstack/react-router'

import { Dialog, DialogContent } from '@/components/ui/dialog'
import { appRoutes } from '@/lib/routes'

const items = [
  { label: 'Overview', to: appRoutes.overview, icon: LayoutDashboard },
  { label: 'Work', to: appRoutes.work, icon: ListTodo },
  { label: 'Projects', to: appRoutes.projects, icon: FolderKanban },
  { label: 'Sessions', to: appRoutes.sessions, icon: MonitorPlay },
]

export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const navigate = useNavigate()

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl p-0">
        <Command className="overflow-hidden rounded-[1.75rem] bg-transparent">
          <div className="flex items-center gap-3 border-b border-white/10 px-4">
            <Search className="size-4 text-[var(--muted-foreground)]" />
            <Command.Input className="h-14 w-full bg-transparent text-sm outline-none placeholder:text-[var(--muted-foreground)]" placeholder="Jump to a section..." />
          </div>
          <Command.List className="max-h-[360px] overflow-auto p-2">
            <Command.Empty className="p-4 text-sm text-[var(--muted-foreground)]">No results.</Command.Empty>
            <Command.Group heading="Navigate" className="text-xs text-[var(--muted-foreground)]">
              {items.map((item) => {
                const Icon = item.icon
                return (
                  <Command.Item
                    key={item.to}
                    className="flex cursor-pointer items-center gap-3 rounded-2xl px-3 py-3 text-sm text-white outline-none data-[selected=true]:bg-white/8"
                    onSelect={() => {
                      onOpenChange(false)
                      void navigate({ to: item.to })
                    }}
                  >
                    <Icon className="size-4 text-[var(--accent)]" />
                    {item.label}
                  </Command.Item>
                )
              })}
            </Command.Group>
          </Command.List>
        </Command>
      </DialogContent>
    </Dialog>
  )
}
