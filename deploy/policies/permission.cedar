// Permission-axis policies (Cedar). Loaded and hot-reloaded by the Gateway.
// This is illustrative; the MVP permission engine currently hard-codes the same
// rules in Go (internal/engine/permission.go) and will migrate to Cedar in Phase 1.

// Employees may not delete users.
forbid (
  principal,
  action == Action::"database.delete_user",
  resource
)
when { principal has role && principal.role == "employee" };

// Exporting all users is sensitive: require human confirmation.
// (Confirmation is expressed as an obligation/annotation the Gateway maps to CONFIRM.)
permit (
  principal,
  action == Action::"database.export_all_users",
  resource
)
advice { "require_confirmation" };
