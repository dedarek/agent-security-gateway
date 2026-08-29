package activity

// RecentGlobal returns at most n newest steps across all agents, newest first.
func (s *Store) RecentGlobal(n int) []Step {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var all []Step
	for _, src := range s.data {
		all = append(all, src...)
	}
	// newest first
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	if n <= 0 || n > len(all) {
		n = len(all)
	}
	return all[:n]
}
