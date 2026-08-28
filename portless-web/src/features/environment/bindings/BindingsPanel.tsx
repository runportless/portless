import { useEffect, useMemo, useState } from 'react'
import { api, environmentPath, jsonBody } from '../../../api'
import { actionError, type ActionErrorDetails } from '../../../components/ActionError'
import type { Environment, EnvironmentMutation, SourceBinding } from '../../../api/contracts/environments'
import type { MockProfile, MockProfileList } from '../../../api/contracts/mocks'
import type { Project, ProjectSource } from '../../../api/contracts/projects'
import { ConfigureCheckoutModal, RemoveCheckoutModal } from '../../SourceModals'
import { CheckoutTable } from './CheckoutTable'
import { ConfigureProviderDialog } from './ConfigureProviderDialog'
import { ProviderBindingsTable } from './ProviderBindingsTable'
import type { EnvironmentCheckoutRow } from './bindingPresentation'

export function BindingsPanel({ environment, project, onNavigate, onChanged }: {
  environment: Environment
  project?: Project
  onNavigate: (path: string) => void
  onChanged: () => void | Promise<void>
}) {
  const [configureTarget, setConfigureTarget] = useState<{ service?: string } | null>(null)
  const [mockProfiles, setMockProfiles] = useState<MockProfile[]>([])
  const [checkoutEdit, setCheckoutEdit] = useState<{ source: ProjectSource; checkout?: SourceBinding } | null>(null)
  const [checkoutRemove, setCheckoutRemove] = useState<{ source: ProjectSource; checkout: SourceBinding; usedBy: string[] } | null>(null)
  const [checkoutMutationBusy, setCheckoutMutationBusy] = useState(false)
  const [checkoutMutationError, setCheckoutMutationError] = useState<ActionErrorDetails | null>(null)
  const [checkoutNotice, setCheckoutNotice] = useState('')
  const environmentIdentity = useMemo(() => ({ project: environment.project, name: environment.name }), [environment.project, environment.name])

  useEffect(() => {
    api<MockProfileList>(environmentPath(environmentIdentity, '/mocks')).then((result) => setMockProfiles(result.mocks)).catch(() => setMockProfiles([]))
  }, [environmentIdentity])

  const openCheckoutEdit = (item: EnvironmentCheckoutRow) => {
    setCheckoutMutationError(null)
    setCheckoutEdit({ source: item.source, checkout: item.checkout })
  }

  const closeCheckoutMutation = () => {
    if (checkoutMutationBusy) return
    setCheckoutEdit(null)
    setCheckoutRemove(null)
    setCheckoutMutationError(null)
  }

  const saveCheckout = async (path: string) => {
    if (!checkoutEdit) return
    setCheckoutMutationBusy(true)
    setCheckoutMutationError(null)
    try {
      const result = await api<EnvironmentMutation>(environmentPath(environment, `/sources/${encodeURIComponent(checkoutEdit.source.name)}`), {
        method: 'PUT',
        ...jsonBody({ path }),
      })
      await onChanged()
      setCheckoutNotice((result.warnings || []).join(' ') || `${checkoutEdit.source.name} now uses ${path}.`)
      setCheckoutEdit(null)
    } catch (reason) {
      setCheckoutMutationError(actionError("Checkout wasn't updated", reason))
    } finally {
      setCheckoutMutationBusy(false)
    }
  }

  const removeCheckout = async () => {
    if (!checkoutRemove) return
    setCheckoutMutationBusy(true)
    setCheckoutMutationError(null)
    try {
      await api<EnvironmentMutation>(environmentPath(environment, `/sources/${encodeURIComponent(checkoutRemove.source.name)}`), { method: 'DELETE' })
      await onChanged()
      setCheckoutNotice(`${checkoutRemove.source.name} is no longer checked out in ${environment.project}/${environment.name}.`)
      setCheckoutRemove(null)
    } catch (reason) {
      setCheckoutMutationError(actionError("Checkout wasn't removed", reason))
    } finally {
      setCheckoutMutationBusy(false)
    }
  }

  return <>
    <div className="experiment-layout bindings-layout">
      {checkoutNotice && <div className="mock-warning source-add-notice"><strong>CHECKOUT CHANGE</strong><span>{checkoutNotice}</span><button type="button" onClick={() => setCheckoutNotice('')}>DISMISS</button></div>}
      <ProviderBindingsTable environment={environment} onConfigure={(service) => setConfigureTarget({ service })} />
      <CheckoutTable
        environment={environment}
        project={project}
        mutationBusy={checkoutMutationBusy}
        onConfigure={openCheckoutEdit}
        onRemove={(item) => { setCheckoutMutationError(null); setCheckoutRemove({ source: item.source, checkout: item.checkout, usedBy: item.usedBy }) }}
        onManageSources={() => onNavigate(`/projects/${encodeURIComponent(environment.project)}`)}
      />
    </div>
    {configureTarget && <ConfigureProviderDialog
      environment={environment}
      project={project}
      initialService={configureTarget.service}
      mockProfiles={mockProfiles}
      onChanged={onChanged}
      onClose={() => setConfigureTarget(null)}
    />}
    {checkoutEdit && <ConfigureCheckoutModal environment={environment} source={checkoutEdit.source} checkout={checkoutEdit.checkout} busy={checkoutMutationBusy} error={checkoutMutationError} onDismissError={() => setCheckoutMutationError(null)} onClose={closeCheckoutMutation} onSave={saveCheckout} />}
    {checkoutRemove && <RemoveCheckoutModal environment={environment} source={checkoutRemove.source} usedBy={checkoutRemove.usedBy} busy={checkoutMutationBusy} error={checkoutMutationError} onDismissError={() => setCheckoutMutationError(null)} onClose={closeCheckoutMutation} onRemove={removeCheckout} />}
  </>
}
