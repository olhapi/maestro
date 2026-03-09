import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowRight, Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'

import { PageHeader } from '@/components/dashboard/page-header'
import { EpicDialog, IssueDialog, ProjectDialog } from '@/components/forms'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { api } from '@/lib/api'
import {
  projectDispatchBadgeClass,
  projectDispatchLabel,
  summaryActiveCount,
  summaryDoneCount,
  summaryTotalCount,
} from '@/lib/projects'
import { appRoutes } from '@/lib/routes'
import type { EpicSummary, ProjectSummary } from '@/lib/types'

function StatCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-[1.25rem] border border-white/8 bg-black/20 p-4">
      <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{label}</p>
      <p className="mt-3 font-display text-3xl text-white">{value}</p>
    </div>
  )
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

  const epicCapableProjects = projects.data.items.filter((project) => project.capabilities?.epics)

  return (
    <div className="grid gap-5">
      <PageHeader
        eyebrow="Portfolio surface"
        title="Projects are now entry points, not dead-end rollups"
        description="Open a project or epic to see execution health, linked work, and recent movement. This page stays focused on choosing the right delivery stream."
        actions={
          <>
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
              disabled={epicCapableProjects.length === 0}
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
          </>
        }
      />

      <div className="grid gap-4 xl:grid-cols-2">
        {projects.data.items.map((project) => {
          const projectEpics = epics.data.items.filter((epic) => epic.project_id === project.id)
          return (
            <Card key={project.id} className="overflow-hidden">
              <CardHeader className="items-start">
                <div className="space-y-3">
                  <Badge>{summaryActiveCount(project)} active</Badge>
                  <div>
                    <CardTitle className="text-2xl">
                      <Link params={{ projectId: project.id }} to={appRoutes.projectDetail}>
                        {project.name}
                      </Link>
                    </CardTitle>
                    <p className="mt-3 max-w-xl text-sm leading-7 text-[var(--muted-foreground)]">{project.description || 'No description yet.'}</p>
                    <p className="mt-2 text-xs text-[var(--muted-foreground)]">{project.repo_path || 'No repo path configured yet.'}</p>
                    <p className="mt-2 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                      {project.provider_kind || 'kanban'}
                      {project.provider_project_ref ? ` · ${project.provider_project_ref}` : ''}
                    </p>
                    {project.dispatch_error ? <p className="mt-2 text-xs text-rose-200">{project.dispatch_error}</p> : null}
                  </div>
                </div>
                <div className="flex gap-2">
                  <Badge className={projectDispatchBadgeClass(project)}>{projectDispatchLabel(project)}</Badge>
                  <Button
                    variant="ghost"
                    onClick={() => {
                      setEditingProject(project)
                      setProjectDialogOpen(true)
                    }}
                  >
                    Edit
                  </Button>
                  <Button variant="ghost" size="icon" onClick={() => deleteProject.mutate(project.id)}>
                    <Trash2 className="size-4" />
                  </Button>
                </div>
              </CardHeader>

              <CardContent className="grid gap-4">
                <div className="grid grid-cols-3 gap-3">
                  <StatCard label="Total" value={String(summaryTotalCount(project))} />
                  <StatCard label="Done" value={String(summaryDoneCount(project))} />
                  <StatCard label="Blocked/active" value={String(summaryActiveCount(project))} />
                </div>

                <div className="rounded-[1.5rem] border border-white/8 bg-black/20 p-4">
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Epics</p>
                      <p className="mt-2 text-sm text-white">{projectEpics.length} delivery arcs</p>
                    </div>
                  </div>
                  <div className="mt-4 grid gap-3">
                    {projectEpics.length === 0 ? (
                      <p className="text-sm text-[var(--muted-foreground)]">No epics yet for this project.</p>
                    ) : (
                      projectEpics.map((epic) => (
                        <div key={epic.id} className="flex items-center justify-between gap-3 rounded-[1.25rem] border border-white/8 bg-white/[0.04] p-4">
                          <div className="min-w-0">
                            <p className="font-medium text-white">
                              <Link params={{ epicId: epic.id }} to={appRoutes.epicDetail}>
                                {epic.name}
                              </Link>
                            </p>
                            <p className="mt-1 text-sm text-[var(--muted-foreground)]">{epic.description || 'No epic description.'}</p>
                          </div>
                          <div className="flex shrink-0 items-center gap-2">
                            <Badge>{summaryActiveCount(epic)} active</Badge>
                            <Button
                              variant="ghost"
                              onClick={() => {
                                setEditingEpic(epic)
                                setEpicDialogOpen(true)
                              }}
                            >
                              Edit
                            </Button>
                            <Button variant="ghost" size="icon" onClick={() => deleteEpic.mutate(epic.id)}>
                              <Trash2 className="size-4" />
                            </Button>
                          </div>
                        </div>
                      ))
                    )}
                  </div>
                </div>

                <div className="flex items-center justify-between rounded-[1.5rem] border border-white/8 bg-[linear-gradient(135deg,rgba(196,255,87,.10),rgba(83,217,255,.06))] p-4">
                  <div>
                    <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Execution hub</p>
                    <p className="mt-2 text-sm text-white">Open the full project page for linked issues, health, and recent work.</p>
                  </div>
                  <Link className="inline-flex items-center gap-2 text-sm text-[var(--accent)]" params={{ projectId: project.id }} to={appRoutes.projectDetail}>
                    Open project
                    <ArrowRight className="size-4" />
                  </Link>
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
        projects={epicCapableProjects}
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
