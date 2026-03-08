import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'

import { EpicDialog, IssueDialog, ProjectDialog } from '@/components/forms'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { api } from '@/lib/api'
import type { EpicSummary, ProjectSummary } from '@/lib/types'

function activeCount(counts: ProjectSummary['counts']) {
  return counts.ready + counts.in_progress + counts.in_review
}

export function ProjectsPage() {
  const queryClient = useQueryClient()
  const bootstrap = useQuery({ queryKey: ['bootstrap'], queryFn: api.bootstrap })
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.listProjects })
  const epics = useQuery({ queryKey: ['epics'], queryFn: () => api.listEpics() })
  const [projectDialogOpen, setProjectDialogOpen] = useState(false)
  const [epicDialogOpen, setEpicDialogOpen] = useState(false)
  const [issueDialogOpen, setIssueDialogOpen] = useState(false)
  const [editingProject, setEditingProject] = useState<ProjectSummary | undefined>()
  const [editingEpic, setEditingEpic] = useState<EpicSummary | undefined>()

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['bootstrap'] }),
      queryClient.invalidateQueries({ queryKey: ['projects'] }),
      queryClient.invalidateQueries({ queryKey: ['epics'] }),
      queryClient.invalidateQueries({ queryKey: ['issues'] }),
    ])
  }

  const deleteProject = useMutation({
    mutationFn: (id: string) => api.deleteProject(id),
    onSuccess: async () => {
      toast.success('Project deleted')
      await invalidate()
    },
  })

  const deleteEpic = useMutation({
    mutationFn: (id: string) => api.deleteEpic(id),
    onSuccess: async () => {
      toast.success('Epic deleted')
      await invalidate()
    },
  })

  if (!projects.data || !epics.data || !bootstrap.data) {
    return <Card className="h-[420px] animate-pulse bg-white/5" />
  }

  return (
    <div className="grid gap-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <Badge>Portfolio surface</Badge>
          <h3 className="mt-4 font-display text-4xl font-semibold">Projects, epics, and issue rollups</h3>
        </div>
        <div className="flex gap-3">
          <Button
            variant="secondary"
            onClick={() => {
              setEditingProject(undefined)
              setProjectDialogOpen(true)
            }}
          >
            <Plus className="size-4" />
            New project
          </Button>
          <Button
            variant="secondary"
            onClick={() => {
              setEditingEpic(undefined)
              setEpicDialogOpen(true)
            }}
          >
            <Plus className="size-4" />
            New epic
          </Button>
          <Button onClick={() => setIssueDialogOpen(true)}>
            <Plus className="size-4" />
            New issue
          </Button>
        </div>
      </div>

      <div className="grid gap-4 xl:grid-cols-2">
        {projects.data.items.map((project) => {
          const projectEpics = epics.data.items.filter((epic) => epic.project_id === project.id)
          return (
            <Card key={project.id}>
              <CardHeader>
                <div>
                  <Badge>{activeCount(project.counts)} active</Badge>
                  <CardTitle className="mt-4">{project.name}</CardTitle>
                  <CardDescription className="mt-2">{project.description || 'No description yet.'}</CardDescription>
                </div>
                <div className="flex gap-2">
                  <Button
                    variant="ghost"
                    onClick={() => {
                      setEditingProject(project)
                      setProjectDialogOpen(true)
                    }}
                  >
                    Edit
                  </Button>
                  <Button variant="ghost" onClick={() => deleteProject.mutate(project.id)}>
                    <Trash2 className="size-4" />
                  </Button>
                </div>
              </CardHeader>
              <CardContent className="space-y-3">
                <div className="grid grid-cols-3 gap-3">
                  <div className="rounded-2xl border border-white/8 bg-black/20 p-4">
                    <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Total</p>
                    <p className="mt-2 font-display text-3xl">{project.counts.backlog + project.counts.ready + project.counts.in_progress + project.counts.in_review + project.counts.done + project.counts.cancelled}</p>
                  </div>
                  <div className="rounded-2xl border border-white/8 bg-black/20 p-4">
                    <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Done</p>
                    <p className="mt-2 font-display text-3xl">{project.counts.done}</p>
                  </div>
                  <div className="rounded-2xl border border-white/8 bg-black/20 p-4">
                    <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Blocked/active</p>
                    <p className="mt-2 font-display text-3xl">{project.counts.ready + project.counts.in_progress + project.counts.in_review}</p>
                  </div>
                </div>
                <div className="space-y-2">
                  {projectEpics.length === 0 ? (
                    <p className="text-sm text-[var(--muted-foreground)]">No epics yet for this project.</p>
                  ) : (
                    projectEpics.map((epic) => (
                      <div key={epic.id} className="flex items-center justify-between rounded-2xl border border-white/8 bg-black/20 p-4">
                        <div>
                          <p className="font-medium text-white">{epic.name}</p>
                          <p className="text-sm text-[var(--muted-foreground)]">{epic.description || 'No epic description.'}</p>
                        </div>
                        <div className="flex items-center gap-2">
                          <Badge>{activeCount(epic.counts)} active</Badge>
                          <Button
                            variant="ghost"
                            onClick={() => {
                              setEditingEpic(epic)
                              setEpicDialogOpen(true)
                            }}
                          >
                            Edit
                          </Button>
                          <Button variant="ghost" onClick={() => deleteEpic.mutate(epic.id)}>
                            <Trash2 className="size-4" />
                          </Button>
                        </div>
                      </div>
                    ))
                  )}
                </div>
              </CardContent>
            </Card>
          )
        })}
      </div>

      <ProjectDialog
        open={projectDialogOpen}
        onOpenChange={setProjectDialogOpen}
        initial={editingProject}
        onSubmit={async (body) => {
          if (editingProject) {
            await api.updateProject(editingProject.id, body)
            toast.success('Project updated')
          } else {
            await api.createProject(body)
            toast.success('Project created')
          }
          await invalidate()
        }}
      />

      <EpicDialog
        open={epicDialogOpen}
        onOpenChange={setEpicDialogOpen}
        initial={editingEpic}
        projects={projects.data.items}
        onSubmit={async (body) => {
          if (editingEpic) {
            await api.updateEpic(editingEpic.id, body)
            toast.success('Epic updated')
          } else {
            await api.createEpic(body)
            toast.success('Epic created')
          }
          await invalidate()
        }}
      />

      <IssueDialog
        open={issueDialogOpen}
        onOpenChange={setIssueDialogOpen}
        projects={projects.data.items}
        epics={epics.data.items}
        onSubmit={async (body) => {
          await api.createIssue(body)
          toast.success('Issue created')
          await invalidate()
        }}
      />
    </div>
  )
}
