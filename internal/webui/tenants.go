package webui

// tenantIndex is a minimal tenant lookup the registry API uses to validate
// tenant names exist.
type TenantNames struct {
	Fn func() []string
}

func (t *TenantNames) names() []string {
	if t == nil || t.Fn == nil {
		return nil
	}
	return t.Fn()
}

func (t *TenantNames) exists(name string) bool {
	for _, n := range t.names() {
		if n == name {
			return true
		}
	}
	return false
}
