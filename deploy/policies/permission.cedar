// Permission-axis policies (Cedar) — evaluated by cedar-go v1.8.0, the same engine
// ToolHive uses (pkg/authz/authorizers/cedar). Entity/request shape mirrors
// ToolHive: principal Client::"<agent>", action Action::"call_tool"|"auto_execute",
// resource Tool::"<tool_id>", principal.role attribute drives RBAC.
//
// Two actions implement Bifrost's execute-vs-auto-execute split:
//   call_tool     — may the tool run at all?      (deny => BLOCK)
//   auto_execute  — may it run WITHOUT approval?   (deny => CONFIRM / human-in-the-loop)

// Baseline: allow by default (Cedar is deny-by-default, so we need an explicit permit).
permit (principal, action, resource);

// Employees may not delete users — hard BLOCK.
forbid (
  principal,
  action == Action::"call_tool",
  resource == Tool::"database.delete_user"
)
when { principal.role == "employee" };

// Exporting all users is executable but NOT auto-executable => requires human CONFIRM.
forbid (
  principal,
  action == Action::"auto_execute",
  resource == Tool::"database.export_all_users"
);
