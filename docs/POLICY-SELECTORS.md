# Structured capability policy

The legacy `agent_id + rule_id + action` policy remains supported. New policies may
also carry a JSON `selector` in the same row. The gateway, not the web client, is
the enforcement point.

## Selector fields

```json
{
  "kind": "capability | mcp | skill",
  "feature": "server | tool | resource | prompt | skill",
  "capability": "shell | filesystem | network",
  "server": "github",
  "tool": "create_issue",
  "operation": "list | get | call | read | write | delete",
  "target": "*.example.com",
  "path_class": "workspace | sensitive",
  "command_class": "readonly | network | privileged | destructive",
  "data_class": "public | sensitive"
}
```

Empty fields are wildcards. `target` accepts Go path-style globs. A malformed
selector is ignored rather than widened into a global rule.

## Precedence

1. Agent-specific rules beat global rules.
2. Structured selectors beat legacy tool-wide rules.
3. Within the same scope, the rule with more matching dimensions wins.
4. A tie resolves `BLOCK > CONFIRM > ALLOW`.
5. No matching override falls through to the built-in gateway/Rampart risk model.

## MCP vocabulary

The control plane deliberately separates:

- `mcp/server/connect`: server admission;
- `mcp/tool/call`: a tool invocation, including the current ingress' legacy
  `write` receipt mapping;
- `mcp/resource/read`: Resource reads;
- `mcp/prompt/get`: Prompt retrieval;
- `skill/skill/load`: Skill activation.

The current vertical slice enforces the selector on the gateway Pre decision path.
The MCP `tools/list` response filter and a loader-level Skill admission hook must
be connected to this same policy catalog before they can be advertised as
complete. Discovery of a new server/tool/skill never grants access by itself.

## Borrowed design boundaries

This is intentionally a small adapter, not a new policy language:

- [ToolHive authorization](https://github.com/StacklokLabs/toolhive/blob/main/docs/authz.md)
  supplies the feature/operation/resource split and default-deny discovery
  boundary.
- [Bifrost MCP plugin contract](https://github.com/maximhq/bifrost) separates
  execution from automatic execution/approval.
- [Pipelock](https://github.com/larsks/pipelock) motivates target, SSRF and data
  egress conditions.
- [Invariant](https://github.com/invariantlabs-ai/invariant) motivates keeping
  future sequence/data-flow checks outside the simple allow/confirm/block row.

No third-party package is vendored; these references define the boundary and the
existing gateway remains the only runtime enforcement point.
