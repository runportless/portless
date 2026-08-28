import { useEffect, useMemo, useRef, useState } from 'react'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../components/ActionError'
import type { Environment } from '../../api/contracts/environments'
import {
  buildMCPCommand,
  defaultMCPCapabilities,
  mcpAccessLabel,
  mcpToolCount,
  serializeMCPClientConfiguration,
  suggestedMCPServerName,
  validMCPConfiguration,
  type MCPCapabilities,
  type MCPConfiguration,
  type MCPScope,
} from './mcpConfiguration'

type ScopeKind = MCPScope['kind']
type PreviewFormat = 'json' | 'command'

export function MCPSettings({ environments, initialEnvironment }: {
  environments: Environment[]
  initialEnvironment?: string
}) {
  const environmentChoices = useMemo(() => [...environments]
    .sort((left, right) => `${left.project}/${left.name}`.localeCompare(`${right.project}/${right.name}`))
    .map((environment) => ({ selector: `${environment.project}/${environment.name}`, status: environment.status })), [environments])
  const workspaceChoices = useMemo(() => {
    const choices = new Map<string, string>()
    for (const environment of environments) {
      for (const source of environment.sources || []) {
        if (source.path && !choices.has(source.path)) choices.set(source.path, `${source.name} · ${environment.project}/${environment.name}`)
      }
    }
    return [...choices].map(([directory, label]) => ({ directory, label })).sort((left, right) => left.label.localeCompare(right.label))
  }, [environments])
  const requestedEnvironment = environmentChoices.some((choice) => choice.selector === initialEnvironment) ? initialEnvironment : undefined
  const defaultEnvironment = requestedEnvironment || (environmentChoices.length === 1 ? environmentChoices[0].selector : '')
  const defaultWorkspace = workspaceChoices.length === 1 ? workspaceChoices[0].directory : ''
  const [scopeKind, setScopeKind] = useState<ScopeKind>('environment')
  const [environment, setEnvironment] = useState(defaultEnvironment)
  const [workspace, setWorkspace] = useState(defaultWorkspace)
  const [serverName, setServerName] = useState(() => suggestedMCPServerName({ kind: 'environment', environment: defaultEnvironment }))
  const [serverNameEdited, setServerNameEdited] = useState(false)
  const [executable, setExecutable] = useState('portless')
  const [capabilities, setCapabilities] = useState<MCPCapabilities>({ ...defaultMCPCapabilities })
  const [format, setFormat] = useState<PreviewFormat>('json')
  const [copied, setCopied] = useState(false)
  const [copyError, setCopyError] = useState<ActionErrorDetails | null>(null)
  const copyReset = useRef<number | undefined>(undefined)

  useEffect(() => () => window.clearTimeout(copyReset.current), [])
  useEffect(() => {
    if (!requestedEnvironment || requestedEnvironment === environment) return
    setEnvironment(requestedEnvironment)
    if (!serverNameEdited) setServerName(suggestedMCPServerName({ kind: 'environment', environment: requestedEnvironment }))
  }, [environment, requestedEnvironment, serverNameEdited])

  const scope = scopeFor(scopeKind, environment, workspace)
  const configuration: MCPConfiguration = { serverName, executable, scope, capabilities }
  const valid = validMCPConfiguration(configuration)
  const preview = valid ? (format === 'json' ? serializeMCPClientConfiguration(configuration) : buildMCPCommand(configuration)) : ''
  const access = mcpAccessLabel(capabilities)
  const tools = mcpToolCount(capabilities)
  const elevated = capabilities.lifecycle || capabilities.trafficControl

  const changeScope = (next: ScopeKind) => {
    setScopeKind(next)
    if (!serverNameEdited) setServerName(suggestedMCPServerName(scopeFor(next, environment, workspace)))
  }

  const changeEnvironment = (next: string) => {
    setEnvironment(next)
    if (!serverNameEdited) setServerName(suggestedMCPServerName({ kind: 'environment', environment: next }))
  }

  const changeWorkspace = (next: string) => {
    setWorkspace(next)
    if (!serverNameEdited) setServerName(suggestedMCPServerName({ kind: 'workspace', directory: next }))
  }

  const toggleCapability = (capability: keyof MCPCapabilities) => {
    setCapabilities((current) => ({ ...current, [capability]: !current[capability] }))
  }

  const copy = async () => {
    if (!preview) return
    setCopyError(null)
    try {
      await navigator.clipboard.writeText(preview)
      setCopied(true)
      window.clearTimeout(copyReset.current)
      copyReset.current = window.setTimeout(() => setCopied(false), 1600)
    } catch (error) {
      setCopyError(actionError('Could not copy the MCP configuration', error))
    }
  }

  return <section className="panel settings-panel settings-panel--mcp-config">
      <div className="panel-title"><span>CONFIGURE CLIENT</span></div>
      <div className="settings-section settings-section--mcp">
        <div className="settings-copy"><div className="eyebrow">ACCESS</div><p>Choose the environments and capabilities available to your MCP client.</p></div>
        <div className="mcp-configurator">
        <div className="mcp-form">
          <div className="mcp-field-pair">
            <label><span>SERVER NAME</span><input name="portless-mcp-server-name" autoComplete="off" spellCheck={false} maxLength={80} value={serverName} onChange={(event) => { setServerNameEdited(true); setServerName(event.target.value) }} /></label>
            <label><span>EXECUTABLE</span><input name="portless-mcp-executable" autoComplete="off" spellCheck={false} value={executable} onChange={(event) => setExecutable(event.target.value)} /><small>Use an absolute path if your desktop client cannot resolve <code>portless</code>.</small></label>
          </div>

          <fieldset className="mcp-choice-group">
            <legend>SCOPE</legend>
            <label className={scopeKind === 'environment' ? 'is-selected' : ''}><input type="radio" name="mcp-scope" checked={scopeKind === 'environment'} onChange={() => changeScope('environment')} /><span><strong>Environment</strong><small>Pin access to one named environment.</small></span></label>
            <label className={`${scopeKind === 'workspace' ? 'is-selected ' : ''}${workspaceChoices.length === 0 ? 'is-disabled' : ''}`}><input type="radio" name="mcp-scope" checked={scopeKind === 'workspace'} disabled={workspaceChoices.length === 0} onChange={() => changeScope('workspace')} /><span><strong>Workspace</strong><small>Use environments associated with one source checkout.</small></span></label>
            <label className={scopeKind === 'all' ? 'is-selected' : ''}><input type="radio" name="mcp-scope" checked={scopeKind === 'all'} onChange={() => changeScope('all')} /><span><strong>All environments</strong><small>Permit access across this Portless installation.</small></span></label>
          </fieldset>

          {scopeKind === 'environment' && <label className="mcp-scope-value"><span>ENVIRONMENT</span><select aria-label="MCP environment" value={environment} onChange={(event) => changeEnvironment(event.target.value)}><option value="">Select an environment</option>{environmentChoices.map((choice) => <option value={choice.selector} key={choice.selector}>{choice.selector} · {choice.status}</option>)}</select></label>}
          {scopeKind === 'workspace' && <label className="mcp-scope-value"><span>WORKSPACE SOURCE</span><select aria-label="MCP workspace source" value={workspace} onChange={(event) => changeWorkspace(event.target.value)}><option value="">Select a source checkout</option>{workspaceChoices.map((choice) => <option value={choice.directory} key={choice.directory}>{choice.label} · {choice.directory}</option>)}</select><small>The generated generic configuration uses <code>cwd</code>; verify that your MCP client supports it.</small></label>}

          <div className="mcp-capabilities-group">
            <div className="mcp-capabilities__title">CAPABILITIES</div>
            <div className="mcp-capabilities" role="group" aria-label="Capabilities">
            <label><input type="checkbox" checked disabled /><span><strong>Inspection</strong><small>Environments, services, logs, traffic summaries, recordings, faults, operations, and timeline.</small></span><b>ALWAYS</b></label>
            <label><input type="checkbox" checked={capabilities.lifecycle} onChange={() => toggleCapability('lifecycle')} /><span><strong>Lifecycle</strong><small>Start or stop environments and change service state.</small></span><b>+3 TOOLS</b></label>
            <label><input type="checkbox" checked={capabilities.trafficControl} onChange={() => toggleCapability('trafficControl')} /><span><strong>Traffic control</strong><small>Start or stop recordings and apply or disable bounded faults.</small></span><b>+5 TOOLS</b></label>
            <label><input type="checkbox" checked={capabilities.sensitiveTraffic} onChange={() => toggleCapability('sensitiveTraffic')} /><span><strong>Sensitive traffic</strong><small>Read bounded headers and captured request, response, or decoded message payload prefixes.</small></span><b>+1 TOOL</b></label>
            </div>
          </div>

          <div className="mcp-notices" aria-live="polite">
            {scopeKind === 'all' && <div className="mcp-notice"><strong>INSTALLATION-WIDE SCOPE</strong><span>This configuration can access every environment in this Portless installation.</span></div>}
            {elevated && <div className="mcp-notice"><strong>OPERATIONAL ACCESS</strong><span>This configuration permits the MCP client to change local environment behavior.</span></div>}
            {capabilities.sensitiveTraffic && <div className="mcp-notice"><strong>SENSITIVE DATA</strong><span>Traffic detail may contain credentials, personal data, or application data even after standard header redaction.</span></div>}
          </div>
        </div>

        <div className="mcp-preview">
          <header>
            <div><span>GENERATED CONFIGURATION</span><strong className={access === 'READ ONLY' ? '' : 'warning-text'}>{access} · {tools} TOOLS</strong></div>
            <button className="button button--small" type="button" disabled={!valid} onClick={() => void copy()}>{copied ? 'COPIED' : 'COPY'}</button>
          </header>
          <div className="mcp-format" role="tablist" aria-label="MCP configuration format">
            <button type="button" role="tab" aria-selected={format === 'json'} className={format === 'json' ? 'is-active' : ''} onClick={() => setFormat('json')}>JSON</button>
            <button type="button" role="tab" aria-selected={format === 'command'} className={format === 'command' ? 'is-active' : ''} onClick={() => setFormat('command')}>COMMAND</button>
          </div>
          {valid ? <pre aria-label="Generated MCP configuration">{preview}</pre> : <div className="mcp-preview__empty">Choose a scope and complete the required fields to generate a configuration.</div>}
          <p>Paste this into your MCP client configuration and reload the client. Copying it does not launch a process or change Portless.</p>
          {copyError && <ActionErrorNotice error={copyError} onDismiss={() => setCopyError(null)} />}
        </div>
      </div>
      </div>
    </section>
}

function scopeFor(kind: ScopeKind, environment: string, workspace: string): MCPScope {
  if (kind === 'environment') return { kind, environment }
  if (kind === 'workspace') return { kind, directory: workspace }
  return { kind }
}
