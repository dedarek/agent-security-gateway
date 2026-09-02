export type PolicySelector = Record<string, string>

export type ControlCapability = {
  rule_id: string
  label: string
  desc: string
  l2?: boolean
  selector: PolicySelector
}

export type CapabilityGroup = {
  id: string
  label: string
  hint: string
  items: ControlCapability[]
}

// The catalog is deliberately data-only. Enforcement lives in gateway/Rampart;
// these selectors are sent to /api/policies and are not client-side security.
export const CAPABILITY_GROUPS: CapabilityGroup[] = [
  {
    id: 'runtime',
    label: '本机能力',
    hint: '按动作、命令类别、路径和数据敏感度细分',
    items: [
      { rule_id: 'Bash', label: 'Shell 执行', desc: 'Shell 命令入口（未分类命令）', l2: true, selector: { kind: 'capability', capability: 'shell', operation: 'execute' } },
      { rule_id: 'Bash:readonly', label: 'Shell · 只读命令', desc: 'ls、pwd、git status 等不改变状态的命令', selector: { kind: 'capability', capability: 'shell', command_class: 'readonly' } },
      { rule_id: 'Bash:network', label: 'Shell · 网络命令', desc: 'curl、wget、ssh、scp、nc 等外联命令', l2: true, selector: { kind: 'capability', capability: 'shell', command_class: 'network' } },
      { rule_id: 'Bash:privileged', label: 'Shell · 提权/权限', desc: 'sudo、chmod、chown、setfacl 等', l2: true, selector: { kind: 'capability', capability: 'shell', command_class: 'privileged' } },
      { rule_id: 'Bash:destructive', label: 'Shell · 破坏性命令', desc: '删除、磁盘写入、终止进程、关机等', l2: true, selector: { kind: 'capability', capability: 'shell', command_class: 'destructive' } },
      { rule_id: 'WebFetch', label: '网络外发', desc: '向外部 URL 发起请求', l2: true, selector: { kind: 'capability', capability: 'network', feature: 'tool', tool: 'WebFetch' } },
      { rule_id: 'WebFetch:upload', label: '网络 · 上传数据', desc: 'POST/PUT 或请求体包含本地数据', l2: true, selector: { kind: 'capability', capability: 'network', tool: 'WebFetch', operation: 'call', data_class: 'sensitive' } },
      { rule_id: 'WebFetch:target', label: '网络 · 目标地址', desc: '按域名、URL 或内网目标单独设规则', selector: { kind: 'capability', capability: 'network', tool: 'WebFetch', target: '*' } },
      { rule_id: 'Write', label: '写文件', desc: '创建或覆盖文件', selector: { kind: 'capability', capability: 'filesystem', tool: 'Write', operation: 'write' } },
      { rule_id: 'Write:sensitive', label: '写文件 · 敏感路径', desc: '.env、SSH、凭证和系统配置等', l2: true, selector: { kind: 'capability', capability: 'filesystem', tool: 'Write', path_class: 'sensitive' } },
      { rule_id: 'Edit', label: '改文件', desc: '编辑已有文件', selector: { kind: 'capability', capability: 'filesystem', tool: 'Edit', operation: 'write' } },
      { rule_id: 'Read', label: '读文件', desc: '读取本地文件', selector: { kind: 'capability', capability: 'filesystem', tool: 'Read', operation: 'read' } },
      { rule_id: 'Read:sensitive', label: '读文件 · 敏感路径', desc: '读取凭证、密钥、SSH 或系统敏感路径', l2: true, selector: { kind: 'capability', capability: 'filesystem', tool: 'Read', path_class: 'sensitive' } },
      { rule_id: 'WebSearch', label: '联网搜索', desc: '发起搜索；查询词仍会经过敏感数据检测', selector: { kind: 'capability', capability: 'network', tool: 'WebSearch', operation: 'call' } },
    ],
  },
  {
    id: 'mcp',
    label: 'MCP / Skills',
    hint: '服务、工具、资源、Prompt 和 Skill 分开授权；默认不因发现新工具而放行',
    items: [
      { rule_id: 'MCP:server', label: 'MCP · 服务接入', desc: '允许 Agent 连接指定 MCP server', l2: true, selector: { kind: 'mcp', feature: 'server', operation: 'connect', server: '*' } },
      { rule_id: 'MCP:tool', label: 'MCP · 工具调用', desc: '允许调用 MCP tool；可再按 server/tool 精确收窄', l2: true, selector: { kind: 'mcp', feature: 'tool', operation: 'call', server: '*', tool: '*' } },
      { rule_id: 'MCP:resource', label: 'MCP · Resource 读取', desc: '读取资源 URI，与 tools/call 分开控制', selector: { kind: 'mcp', feature: 'resource', operation: 'read', server: '*' } },
      { rule_id: 'MCP:prompt', label: 'MCP · Prompt 获取', desc: '读取 MCP Prompt 模板', selector: { kind: 'mcp', feature: 'prompt', operation: 'get', server: '*' } },
      { rule_id: 'Skill:load', label: 'Skill · 加载/启用', desc: '加载 Agent Skill；新 Skill 不继承旧 Skill 的授权', l2: true, selector: { kind: 'skill', feature: 'skill', operation: 'load', target: '*' } },
    ],
  },
]

export const CAPS = CAPABILITY_GROUPS.flatMap((group) => group.items)
