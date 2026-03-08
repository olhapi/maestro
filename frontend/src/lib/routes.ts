export const appRoutes = {
  overview: '/',
  work: '/work',
  projects: '/projects',
  projectDetail: '/projects/$projectId',
  epicDetail: '/epics/$epicId',
  issueDetail: '/issues/$identifier',
  sessions: '/sessions',
} as const

export function isProjectsPath(pathname: string) {
  return pathname === appRoutes.projects || pathname.startsWith('/projects/') || pathname.startsWith('/epics/')
}
