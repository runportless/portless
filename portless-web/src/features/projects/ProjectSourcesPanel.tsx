import { useEffect, useRef, useState } from 'react'
import { api, jsonBody, projectPath } from '../../api'
import { actionError, type ActionErrorDetails } from '../../components/ActionError'
import { paginateItems, PanelPagination } from '../../components/PanelPagination'
import { StatusMark } from '../../components/Status'
import type { Environment, Project, ProjectSource } from '../../types'
import { AddProjectSourceModal, DeleteProjectSourceModal } from '../SourceModals'
import { projectSourceRows, projectSourceStatus } from './projectPresentation'

type ProjectSourceMutation = { warnings: string[]; configurationRequired: string[] }

export function ProjectSourcesPanel({ project, environments, onChanged }: {
  project: Project
  environments: Environment[]
  onChanged: () => Promise<void>
}) {
  const [createOpen, setCreateOpen] = useState(false)
  const [sourceDelete, setSourceDelete] = useState<ProjectSource | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const [notice, setNotice] = useState('')
  const [page, setPage] = useState(0)
  const createButton = useRef<HTMLButtonElement>(null)
  const sourceRows = projectSourceRows(project, environments)
  const pagination = paginateItems(sourceRows, page, 10)

  useEffect(() => {
    setPage(0)
    setCreateOpen(false)
    setSourceDelete(null)
    setError(null)
    setNotice('')
  }, [project.name])

  const closeMutation = () => {
    if (busy) return
    setCreateOpen(false)
    setSourceDelete(null)
    setError(null)
  }
  const addSource = async (name: string, path: string, environment: string) => {
    setBusy(true)
    setError(null)
    try {
      const result = await api<ProjectSourceMutation>(projectPath(project.name, '/sources'), {
        method: 'POST', ...jsonBody({ name, path, environment }),
      })
      await onChanged()
      const notes = [...(result.warnings || [])]
      if (result.configurationRequired?.length) notes.push(`Configure ${name} in ${result.configurationRequired.join(', ')}.`)
      setNotice(notes.join(' ') || `${name} was added to ${project.name} and its checkout was configured for ${environment}.`)
      setCreateOpen(false)
    } catch (reason) {
      setError(actionError("Source wasn't added", reason))
    } finally {
      setBusy(false)
    }
  }
  const deleteSource = async () => {
    if (!sourceDelete) return
    setBusy(true)
    setError(null)
    try {
      await api(projectPath(project.name, `/sources/${encodeURIComponent(sourceDelete.name)}`), { method: 'DELETE' })
      await onChanged()
      setNotice(`${sourceDelete.name} was deleted from ${project.name}.`)
      setSourceDelete(null)
    } catch (reason) {
      setError(actionError("Source wasn't deleted", reason))
    } finally {
      setBusy(false)
    }
  }

  return <>
    {notice && <div className="mock-warning source-add-notice"><strong>SOURCE CHANGE</strong><span>{notice}</span><button type="button" onClick={() => setNotice('')}>DISMISS</button></div>}
    <section className="panel project-sources-panel">
      <div className="panel-title"><span>SOURCES</span><button ref={createButton} className="button button--primary button--small panel-create-button" type="button" aria-haspopup="dialog" onClick={() => { setError(null); setCreateOpen(true) }}>ADD SOURCE</button></div>
      <div className="table-row table-row--header project-source-row"><span>Name</span><span>Path</span><span>Services</span><span aria-hidden="true" /></div>
      {pagination.items.map((source) => <div className="table-row project-source-row" key={source.name}>
        <div className="checkout-source"><StatusMark status={projectSourceStatus(source)} label={false} /><strong>{source.name}</strong></div>
        <div className="project-source-bindings">{source.checkouts.map((checkout) => <div className="project-source-binding" key={checkout.path}>
          <code className="truncate" title={checkout.path}>{checkout.path}</code>
        </div>)}{source.unbound.map((environment) => <div className="project-source-binding" key={`unbound-${environment.name}`}>
          <span className={environment.configurationRequired ? 'warning-text truncate' : 'muted truncate'}>{environment.configurationRequired ? 'configuration required' : 'not bound locally'}</span>
        </div>)}</div>
        <span className="muted truncate" title={source.services?.join(', ')}>{source.services?.join(', ') || '—'}</span>
        <div className="table-row-actions"><button type="button" disabled={busy} onClick={() => { setError(null); setSourceDelete(source) }}>DELETE</button></div>
      </div>)}
      {sourceRows.length === 0 && <div className="empty-row">No sources are registered with this project.</div>}
      <PanelPagination label="sources" pagination={pagination} onPage={setPage} />
    </section>
    {createOpen && <AddProjectSourceModal project={project} environments={environments} busy={busy} error={error} onDismissError={() => setError(null)} onClose={closeMutation} onAdd={addSource} />}
    {sourceDelete && <DeleteProjectSourceModal project={project} source={sourceDelete} environments={environments} busy={busy} error={error} restoreFocusRef={createButton} onDismissError={() => setError(null)} onClose={closeMutation} onDelete={deleteSource} />}
  </>
}
